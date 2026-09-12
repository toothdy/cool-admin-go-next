package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

const sdkModule = "github.com/toothdy/cool-admin-go-next"

type sdkDependency struct {
	version string
	replace string
}

func initializeProject(_ context.Context, directory, module string) error {
	if err := validateModulePath(module); err != nil {
		return err
	}
	if err := ensureEmptyDir(directory); err != nil {
		return err
	}
	dependency, err := resolveSDKDep()
	if err != nil {
		return err
	}
	manifest := bundle.Manifest{
		SchemaVersion: bundle.SchemaVersion,
		Name:          "Echo 插件",
		Key:           "echo-plugin",
		Singleton:     true,
		Version:       "1.0.0",
		Description:   "Cool Go 插件示例",
		Author:        "COOL",
		Readme:        "README.md",
		Runtime: bundle.Runtime{
			ABI:            protocol.Name,
			Module:         bundle.ModuleFile,
			MinHostVersion: "2.0.0",
		},
		Config: map[string]json.RawMessage{"prefix": json.RawMessage("\"echo: \"")},
	}
	manifestData, err := encodeManifest(manifest)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"go.mod":            []byte(renderGoModule(module, dependency)),
		bundle.ManifestFile: manifestData,
		"README.md":         []byte(renderReadme()),
		"plugin/plugin.go":  []byte(renderPlugin()),
		"cmd/wasm/main.go":  []byte(renderBridge(module)),
	}
	for name, content := range files {
		filePath := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return fmt.Errorf("创建脚手架目录失败: %w", err)
		}
		if err := os.WriteFile(filePath, content, 0o644); err != nil {
			return fmt.Errorf("写入脚手架文件 %q 失败: %w", name, err)
		}
	}

	return nil
}

func ensureEmptyDir(directory string) error {
	entries, err := os.ReadDir(directory)
	switch {
	case err == nil && len(entries) > 0:
		return fmt.Errorf("目标目录 %q 不为空，拒绝覆盖", directory)
	case err == nil:
		return nil
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("创建目标目录失败: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("读取目标目录失败: %w", err)
	}
}

func resolveSDKDep() (sdkDependency, error) {
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") {
		return sdkDependency{version: info.Main.Version}, nil
	}
	repository, err := sourceRepository()
	if err != nil {
		return sdkDependency{}, err
	}

	return sdkDependency{version: "v0.0.0", replace: repository}, nil
}

func sourceRepository() (string, error) {
	_, sourceFile, _, ok := goruntime.Caller(0)
	if !ok {
		return "", errors.New("无法定位开发版 SDK 源码，请使用已发布的 cool-plugin")
	}
	repository, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	if err != nil {
		return "", fmt.Errorf("定位开发版 SDK 源码失败: %w", err)
	}
	goModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil || !strings.Contains(string(goModule), "module "+sdkModule) {
		return "", errors.New("无法定位开发版 SDK 源码，请使用已发布的 cool-plugin")
	}

	return repository, nil
}

func renderGoModule(module string, dependency sdkDependency) string {
	content := fmt.Sprintf("module %s\n\ngo 1.26.0\n\nrequire %s %s\n", module, sdkModule, dependency.version)
	if dependency.replace != "" {
		content += fmt.Sprintf("\nreplace %s => %s\n", sdkModule, strconv.Quote(filepath.ToSlash(dependency.replace)))
	}

	return content
}

func renderPlugin() string {
	tag := string(rune(96))

	return fmt.Sprintf(
		"package plugin\n\n"+
			"import (\n\t\"context\"\n\n\t\"github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk\"\n)\n\n"+
			"type Config struct {\n\tPrefix string %sjson:\"prefix\"%s\n}\n\n"+
			"type EchoRequest struct {\n\tValue string %sjson:\"value\"%s\n}\n\n"+
			"type EchoResponse struct {\n\tValue string %sjson:\"value\"%s\n}\n\n"+
			"// Plugin 返回插件定义。\n"+
			"func Plugin() sdk.Definition {\n\treturn sdk.Define(sdk.Method(\"echo\", echo))\n}\n\n"+
			"func echo(ctx context.Context, request EchoRequest) (EchoResponse, error) {\n"+
			"\tconfig, err := sdk.Config[Config](ctx)\n"+
			"\tif err != nil {\n\t\treturn EchoResponse{}, err\n\t}\n\n"+
			"\treturn EchoResponse{Value: config.Prefix + request.Value}, nil\n}\n",
		tag,
		tag,
		tag,
		tag,
		tag,
		tag,
	)
}

func renderBridge(module string) string {
	return fmt.Sprintf(
		"//go:build wasip1\n\n"+
			"package main\n\n"+
			"import (\n\t\"unsafe\"\n\n\t\"%s/plugin\"\n"+
			"\t\"github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk\"\n)\n\n"+
			"func init() {\n\tsdk.Register(plugin.Plugin())\n}\n\n"+
			"//go:wasmexport cool_abi_version\n"+
			"func coolABIVersion() uint32 {\n\treturn sdk.ABIVersion()\n}\n\n"+
			"//go:wasmexport cool_alloc\n"+
			"func coolAlloc(size int32) unsafe.Pointer {\n\treturn sdk.ABIAlloc(size)\n}\n\n"+
			"//go:wasmexport cool_free\n"+
			"func coolFree(pointer unsafe.Pointer, size int32) {\n\tsdk.ABIFree(pointer, size)\n}\n\n"+
			"//go:wasmexport cool_init\n"+
			"func coolInit(invocationID int64, configPointer unsafe.Pointer, configSize int32) int64 {\n"+
			"\treturn sdk.ABIInit(invocationID, configPointer, configSize)\n}\n\n"+
			"//go:wasmexport cool_invoke\n"+
			"func coolInvoke(invocationID int64, methodPointer unsafe.Pointer, methodSize int32, inputPointer unsafe.Pointer, inputSize int32) int64 {\n"+
			"\treturn sdk.ABIInvoke(invocationID, methodPointer, methodSize, inputPointer, inputSize)\n}\n\n"+
			"//go:wasmexport cool_shutdown\n"+
			"func coolShutdown(invocationID int64) int64 {\n\treturn sdk.ABIShutdown(invocationID)\n}\n\n"+
			"//go:wasmexport cool_response_pointer\n"+
			"func coolResponsePointer(handle int64) unsafe.Pointer {\n\treturn sdk.ABIResponsePointer(handle)\n}\n\n"+
			"//go:wasmexport cool_response_length\n"+
			"func coolResponseLength(handle int64) int32 {\n\treturn sdk.ABIResponseLength(handle)\n}\n\n"+
			"//go:wasmexport cool_response_drop\n"+
			"func coolResponseDrop(handle int64) {\n\tsdk.ABIResponseDrop(handle)\n}\n\n"+
			"func main() {}\n",
		module,
	)
}

func renderReadme() string {
	return "# Echo 插件\n\n使用 cool-plugin test、build 和 pack 开发及打包插件。\n"
}
