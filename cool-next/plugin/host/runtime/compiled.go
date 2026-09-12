package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 已编译并通过 ABI 校验的插件
type Compiled struct {
	mu       sync.RWMutex
	runtime  *Runtime
	module   wazero.CompiledModule
	isClosed bool
}

// 创建独立插件实例
func (compiled *Compiled) Instantiate(ctx context.Context) (*Instance, error) {
	return compiled.instantiate(ctx, "")
}

// 创建挂载插件专属 /data 目录的实例
func (compiled *Compiled) InstantiateForPlugin(ctx context.Context, key string) (*Instance, error) {
	if compiled == nil || compiled.runtime == nil {
		return nil, errors.New("已编译插件未初始化")
	}
	dataRoot, err := compiled.runtime.pluginDataRoot(key)
	if err != nil {
		return nil, err
	}

	return compiled.instantiate(ctx, dataRoot)
}

func (compiled *Compiled) instantiate(ctx context.Context, dataRoot string) (*Instance, error) {
	compiled.mu.RLock()
	defer compiled.mu.RUnlock()
	if compiled.isClosed || compiled.runtime == nil || compiled.module == nil {
		return nil, errors.New("已编译插件已关闭")
	}
	moduleConfig := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions(protocol.InitializeExport).
		WithSysWalltime().
		WithSysNanotime()
	if dataRoot != "" {
		moduleConfig = moduleConfig.WithFSConfig(wazero.NewFSConfig().WithDirMount(dataRoot, "/data"))
	}
	module, err := compiled.runtime.wazero.InstantiateModule(ctx, compiled.module, moduleConfig)
	if err != nil {
		return nil, mapRuntimeError(err, protocol.ErrorInitFailed, "实例化 WASM 插件失败")
	}
	bucket := compiled.runtime.host.register(module)
	instance := newInstance(compiled.runtime, module, bucket)
	version, err := instance.abiVersion(ctx)
	if err != nil {
		_ = instance.Close(ctx)
		return nil, err
	}
	if version != protocol.Version {
		_ = instance.Close(ctx)
		return nil, protocol.NewError(protocol.ErrorABIUnsupported, "插件 ABI 版本不兼容")
	}

	return instance, nil
}

// 释放编译产物
func (compiled *Compiled) Close(ctx context.Context) error {
	compiled.mu.Lock()
	defer compiled.mu.Unlock()
	if compiled.isClosed {
		return nil
	}
	compiled.isClosed = true

	return compiled.module.Close(ctx)
}
