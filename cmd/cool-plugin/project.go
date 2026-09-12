package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
)

var (
	modulePattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	artifactFilenameReplacer = strings.NewReplacer("<", "-", ">", "-", ":", "-", `"`, "-", "/", "-", `\`, "-", "|", "-", "?", "-", "*", "-")
)

type artifactProject struct {
	directory    string
	manifest     bundle.Manifest
	manifestData []byte
}

type projectTool struct {
	execute      func(context.Context, string, io.Writer, io.Writer, []string, ...string) error
	validateWASM func(context.Context, []byte) error
}

func newProjectCommands() projectCommands {
	tool := &projectTool{execute: executeGo, validateWASM: validateWASM}

	return projectCommands{
		initialize: initializeProject,
		check:      tool.check,
		test:       tool.test,
		build:      tool.build,
		pack:       tool.pack,
	}
}

func (tool *projectTool) check(ctx context.Context, directory string, _ io.Writer, stderr io.Writer) (artifactProject, error) {
	if directory == "" {
		return artifactProject{}, usageError{message: "命令最多接受一个目录参数"}
	}
	project, err := loadProject(directory)
	if err != nil {
		return artifactProject{}, err
	}
	var version bytes.Buffer
	if err := tool.execute(ctx, project.directory, &version, stderr, nil, "env", "GOVERSION"); err != nil {
		return artifactProject{}, err
	}
	if err := validateGoVersion(strings.TrimSpace(version.String())); err != nil {
		return artifactProject{}, err
	}

	return project, nil
}

func (tool *projectTool) test(ctx context.Context, directory string, stdout, stderr io.Writer) error {
	project, err := tool.check(ctx, directory, stdout, stderr)
	if err != nil {
		return err
	}

	return tool.runTests(ctx, project.directory, stdout, stderr)
}

func (tool *projectTool) build(ctx context.Context, directory string, stdout, stderr io.Writer) error {
	project, err := tool.check(ctx, directory, stdout, stderr)
	if err != nil {
		return err
	}
	if err := tool.runTests(ctx, project.directory, stdout, stderr); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(project.directory, ".plugin-*.wasm")
	if err != nil {
		return fmt.Errorf("创建临时构建文件失败: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("关闭临时构建文件失败: %w", err)
	}
	defer os.Remove(temporaryPath)
	environment := []string{"GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0"}
	if err := tool.execute(
		ctx,
		project.directory,
		stdout,
		stderr,
		environment,
		"build",
		"-buildmode=c-shared",
		"-trimpath",
		"-buildvcs=false",
		"-o",
		temporaryPath,
		"./cmd/wasm",
	); err != nil {
		return err
	}
	wasm, err := os.ReadFile(temporaryPath)
	if err != nil {
		return fmt.Errorf("读取构建产物失败: %w", err)
	}
	if err := tool.validateWASM(ctx, wasm); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(project.directory, bundle.ModuleFile)); err != nil {
		return fmt.Errorf("保存构建产物失败: %w", err)
	}

	return nil
}

func (tool *projectTool) pack(ctx context.Context, directory string, stdout, stderr io.Writer) (string, error) {
	if err := tool.build(ctx, directory, stdout, stderr); err != nil {
		return "", err
	}
	project, err := loadProject(directory)
	if err != nil {
		return "", err
	}
	files, err := collectPackageFiles(project)
	if err != nil {
		return "", err
	}
	packageData, err := bundle.Pack(files)
	if err != nil {
		return "", fmt.Errorf("生成插件包失败: %w", err)
	}
	if _, err := bundle.Read(packageData); err != nil {
		return "", fmt.Errorf("校验插件包失败: %w", err)
	}
	filename := artifactFilenameReplacer.Replace(strings.TrimSpace(project.manifest.Name)) + ".cool"
	output := filepath.Join(project.directory, filename)
	if err := os.WriteFile(output, packageData, 0o644); err != nil {
		return "", fmt.Errorf("写入插件包失败: %w", err)
	}

	return output, nil
}

func (tool *projectTool) runTests(ctx context.Context, directory string, stdout, stderr io.Writer) error {
	return tool.execute(ctx, directory, stdout, stderr, nil, "test", "./...")
}

func loadProject(directory string) (artifactProject, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return artifactProject{}, fmt.Errorf("解析插件目录失败: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return artifactProject{}, fmt.Errorf("读取插件目录失败: %w", err)
	}
	if !info.IsDir() {
		return artifactProject{}, errors.New("插件项目路径必须是目录")
	}
	for _, name := range []string{"go.mod", filepath.Join("cmd", "wasm")} {
		if _, err := os.Stat(filepath.Join(absolute, name)); err != nil {
			return artifactProject{}, fmt.Errorf("插件项目缺少 %s: %w", name, err)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(absolute, bundle.ManifestFile))
	if err != nil {
		return artifactProject{}, fmt.Errorf("读取 plugin.json 失败: %w", err)
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		return artifactProject{}, err
	}
	for field, name := range map[string]string{"logo": manifest.Logo, "readme": manifest.Readme} {
		if name == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(absolute, filepath.FromSlash(name)))
		if err != nil {
			return artifactProject{}, fmt.Errorf("plugin.json %s 引用的文件 %q 不存在: %w", field, name, err)
		}
		if !info.Mode().IsRegular() {
			return artifactProject{}, fmt.Errorf("plugin.json %s 引用的路径 %q 不是普通文件", field, name)
		}
	}

	return artifactProject{directory: absolute, manifest: manifest, manifestData: manifestData}, nil
}

func validateGoVersion(version string) error {
	const prefix = "go1."
	if !strings.HasPrefix(version, prefix) {
		return fmt.Errorf("无法识别 Go 版本 %q", version)
	}
	minorText := strings.TrimPrefix(version, prefix)
	minorEnd := strings.IndexAny(minorText, ".- ")
	if minorEnd >= 0 {
		minorText = minorText[:minorEnd]
	}
	minor := 0
	if _, err := fmt.Sscanf(minorText, "%d", &minor); err != nil || minor < 26 {
		return fmt.Errorf("插件构建需要 Go 1.26 或更高版本，当前为 %s", version)
	}

	return nil
}

func validateWASM(ctx context.Context, wasm []byte) error {
	runtime, err := runtime.New(ctx, runtime.DefaultConfig(), nil)
	if err != nil {
		return fmt.Errorf("创建插件运行时失败: %w", err)
	}
	defer runtime.Close(context.Background())
	compiled, err := runtime.Compile(ctx, wasm)
	if err != nil {
		return fmt.Errorf("校验 plugin.wasm 失败: %w", err)
	}
	defer compiled.Close(context.Background())
	instance, err := compiled.Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("加载 plugin.wasm 失败: %w", err)
	}
	defer instance.Close(context.Background())

	return nil
}

func collectPackageFiles(project artifactProject) (map[string][]byte, error) {
	files := map[string][]byte{bundle.ManifestFile: append([]byte(nil), project.manifestData...)}
	for _, name := range []string{bundle.ModuleFile, project.manifest.Readme, project.manifest.Logo} {
		if name == "" {
			continue
		}
		if err := addPackageFile(files, project.directory, name); err != nil {
			return nil, err
		}
	}
	assets := filepath.Join(project.directory, "assets")
	if _, err := os.Stat(assets); errors.Is(err, os.ErrNotExist) {
		return files, nil
	} else if err != nil {
		return nil, fmt.Errorf("读取 assets 目录失败: %w", err)
	}
	if err := filepath.WalkDir(assets, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("assets 路径 %q 不是普通文件", filePath)
		}
		relative, err := filepath.Rel(project.directory, filePath)
		if err != nil {
			return err
		}

		return addPackageFile(files, project.directory, filepath.ToSlash(relative))
	}); err != nil {
		return nil, fmt.Errorf("收集 assets 失败: %w", err)
	}

	return files, nil
}

func addPackageFile(files map[string][]byte, directory, name string) error {
	if _, exists := files[name]; exists {
		return nil
	}
	filePath := filepath.Join(directory, filepath.FromSlash(name))
	info, err := os.Lstat(filePath)
	if err != nil {
		return fmt.Errorf("读取插件文件 %q 失败: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("插件文件 %q 不是普通文件", name)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取插件文件 %q 失败: %w", name, err)
	}
	files[name] = data

	return nil
}

func executeGo(ctx context.Context, directory string, stdout, stderr io.Writer, environment []string, args ...string) error {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = directory
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = append(os.Environ(), environment...)
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := command.Process.Signal(os.Interrupt); err != nil {
			return err
		}
		return os.ErrProcessDone
	}
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("go %s 已取消: %w", args[0], ctx.Err())
		}
		return fmt.Errorf("go %s 失败: %w", args[0], err)
	}

	return nil
}

func validateModulePath(module string) error {
	if !modulePattern.MatchString(module) || strings.Contains(module, "//") || strings.Contains(module, "/../") || strings.HasSuffix(module, "/") {
		return fmt.Errorf("Go module 路径 %q 不合法", module)
	}
	for _, segment := range strings.Split(module, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("Go module 路径 %q 不合法", module)
		}
	}

	return nil
}

func encodeManifest(manifest bundle.Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码 plugin.json 失败: %w", err)
	}

	return append(data, '\n'), nil
}
