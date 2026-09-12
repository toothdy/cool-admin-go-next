package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// WASM 插件运行时
type Runtime struct {
	mu               sync.RWMutex
	wazero           wazero.Runtime
	compilationCache wazero.CompilationCache
	host             *responseHub
	config           Config
	dataRoot         string
	isClosed         bool
}

// 创建 WASM 插件运行时
func New(ctx context.Context, config Config, handler HostHandler) (*Runtime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dataRoot, err := prepareDataRoot(config.DataRoot)
	if err != nil {
		return nil, err
	}
	runtimeConfig := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(config.MemoryLimitPages).
		WithCloseOnContextDone(true)
	var compilationCache wazero.CompilationCache
	if dataRoot != "" {
		compilationCache, err = wazero.NewCompilationCacheWithDir(filepath.Join(dataRoot, ".wazero-cache"))
		if err != nil {
			return nil, fmt.Errorf("创建 WASM 编译缓存失败: %w", err)
		}
		runtimeConfig = runtimeConfig.WithCompilationCache(compilationCache)
	}
	wazeroRuntime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	runtime := &Runtime{
		wazero:           wazeroRuntime,
		compilationCache: compilationCache,
		host:             newResponseHub(config.MaxPayloadBytes, handler),
		config:           config,
		dataRoot:         dataRoot,
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wazeroRuntime); err != nil {
		_ = wazeroRuntime.Close(ctx)
		if compilationCache != nil {
			_ = compilationCache.Close(ctx)
		}
		return nil, fmt.Errorf("实例化 WASI 失败: %w", err)
	}
	if err := runtime.instantiateHost(ctx); err != nil {
		_ = wazeroRuntime.Close(ctx)
		if compilationCache != nil {
			_ = compilationCache.Close(ctx)
		}
		return nil, err
	}

	return runtime, nil
}

func prepareDataRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("解析插件数据根目录失败: %w", err)
	}
	if err = os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("创建插件数据根目录失败: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("解析插件数据根目录真实路径失败: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("读取插件数据根目录失败: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("插件数据根目录不是目录")
	}

	return realPath, nil
}

func (runtime *Runtime) pluginDataRoot(key string) (string, error) {
	if runtime.dataRoot == "" {
		return "", nil
	}
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, `/\\`) {
		return "", errors.New("插件 key 无效")
	}
	dataRoot := filepath.Join(runtime.dataRoot, key)
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return "", fmt.Errorf("创建插件数据目录失败: %w", err)
	}
	info, err := os.Lstat(dataRoot)
	if err != nil {
		return "", fmt.Errorf("读取插件数据目录失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("插件数据目录不安全")
	}
	realPath, err := filepath.EvalSymlinks(dataRoot)
	if err != nil {
		return "", fmt.Errorf("解析插件数据目录真实路径失败: %w", err)
	}
	relative, err := filepath.Rel(runtime.dataRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("插件数据目录越界")
	}

	return realPath, nil
}

// 编译并校验 WASM 插件
func (runtime *Runtime) Compile(ctx context.Context, wasm []byte) (*Compiled, error) {
	if len(wasm) < 8 || string(wasm[:4]) != "\x00asm" {
		return nil, protocol.NewError(protocol.ErrorABIUnsupported, "插件不是合法 WebAssembly 模块")
	}
	runtime.mu.RLock()
	if runtime.isClosed {
		runtime.mu.RUnlock()
		return nil, errors.New("WASM 插件运行时已关闭")
	}
	compiled, err := runtime.wazero.CompileModule(ctx, wasm)
	runtime.mu.RUnlock()
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorABIUnsupported, "编译 WASM 插件失败", err)
	}
	if err = validateCompiled(compiled); err != nil {
		_ = compiled.Close(ctx)
		return nil, err
	}

	return &Compiled{runtime: runtime, module: compiled}, nil
}

// 关闭运行时及其全部模块
func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.isClosed {
		return nil
	}
	runtime.isClosed = true

	closeErr := runtime.wazero.Close(ctx)
	if runtime.compilationCache != nil {
		closeErr = errors.Join(closeErr, runtime.compilationCache.Close(ctx))
	}

	return closeErr
}

func (runtime *Runtime) instantiateHost(ctx context.Context) error {
	builder := runtime.wazero.NewHostModuleBuilder(protocol.HostModule)
	builder.NewFunctionBuilder().WithFunc(runtime.host.call).Export(protocol.ImportCall)
	builder.NewFunctionBuilder().WithFunc(runtime.host.length).Export(protocol.ImportResponseLength)
	builder.NewFunctionBuilder().WithFunc(runtime.host.read).Export(protocol.ImportResponseRead)
	builder.NewFunctionBuilder().WithFunc(runtime.host.drop).Export(protocol.ImportResponseDrop)
	if _, err := builder.Instantiate(ctx); err != nil {
		return fmt.Errorf("实例化插件 Host API 失败: %w", err)
	}

	return nil
}

func validateCompiled(compiled wazero.CompiledModule) error {
	if len(compiled.ImportedMemories()) > 0 {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件不能导入线性内存")
	}
	if _, exists := compiled.ExportedMemories()[protocol.MemoryExport]; !exists {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件缺少 memory 导出")
	}
	hostSignatures := make(map[string]protocol.FunctionSignature)
	for _, signature := range protocol.HostImports() {
		hostSignatures[signature.Name] = signature
	}
	seenHostImports := make(map[string]bool, len(hostSignatures))
	for _, imported := range compiled.ImportedFunctions() {
		moduleName, name, _ := imported.Import()
		if moduleName != wasi_snapshot_preview1.ModuleName && moduleName != protocol.HostModule {
			return protocol.NewError(protocol.ErrorABIUnsupported, fmt.Sprintf("插件包含非法导入: %s.%s", moduleName, name))
		}
		if moduleName != protocol.HostModule {
			continue
		}
		signature, exists := hostSignatures[name]
		if !exists || seenHostImports[name] {
			return protocol.NewError(protocol.ErrorABIUnsupported, "插件 Host API 导入无效: "+name)
		}
		if !sameValueTypes(imported.ParamTypes(), signature.Parameters) || !sameValueTypes(imported.ResultTypes(), signature.Results) {
			return protocol.NewError(protocol.ErrorABIUnsupported, "插件 Host API 导入签名无效: "+name)
		}
		seenHostImports[name] = true
	}
	for name := range hostSignatures {
		if !seenHostImports[name] {
			return protocol.NewError(protocol.ErrorABIUnsupported, "插件缺少 Host API 导入: "+name)
		}
	}
	exports := compiled.ExportedFunctions()
	for _, signature := range protocol.GuestExports() {
		definition, exists := exports[signature.Name]
		if !exists {
			return protocol.NewError(protocol.ErrorABIUnsupported, "插件缺少导出: "+signature.Name)
		}
		if !sameValueTypes(definition.ParamTypes(), signature.Parameters) || !sameValueTypes(definition.ResultTypes(), signature.Results) {
			return protocol.NewError(protocol.ErrorABIUnsupported, "插件导出签名无效: "+signature.Name)
		}
	}
	initialize, exists := exports[protocol.InitializeExport]
	if !exists || len(initialize.ParamTypes()) != 0 || len(initialize.ResultTypes()) != 0 {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件缺少合法 _initialize 导出")
	}

	return nil
}

func sameValueTypes(actual []api.ValueType, want []protocol.ValueType) bool {
	if len(actual) != len(want) {
		return false
	}
	for index := range actual {
		if actual[index] != byte(want[index]) {
			return false
		}
	}

	return true
}
