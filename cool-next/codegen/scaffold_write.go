package codegen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 待写入工作区的 Go 文件
type CodeFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type preparedCodeFile struct {
	path    string
	content []byte
}

// 受工作区边界约束的开发代码读写能力
type Scaffold struct {
	workspace string
	mu        sync.Mutex
}

// 代码脚手架工具，workspace 是模块代码生成的写入根目录
func NewScaffold(workspace string) (*Scaffold, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, exception.Validate("代码工作区不能为空")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, exception.WrapValidate(err, "解析代码工作区失败")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, exception.WrapValidate(err, "代码工作区不存在")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, exception.WrapValidate(err, "读取代码工作区失败")
	}
	if !info.IsDir() {
		return nil, exception.Validate("代码工作区必须是目录")
	}

	return &Scaffold{workspace: resolved}, nil
}

// 含合法 config.go 的模块名称
func (scaffold *Scaffold) GetModuleTree() ([]string, error) {
	root, err := scaffold.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()

	info, err := root.Lstat("modules")
	if err != nil {
		return nil, exception.WrapCore(err, "读取模块目录失败")
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, exception.Validate("模块目录必须是工作区内的真实目录")
	}
	directory, err := root.Open("modules")
	if err != nil {
		return nil, exception.WrapCore(err, "读取模块目录失败")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, exception.WrapCore(err, "读取模块目录失败")
	}
	modules := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		configPath := path.Join("modules", entry.Name(), "config.go")
		config, configErr := root.Lstat(configPath)
		if configErr != nil || config.Mode()&fs.ModeSymlink != 0 || !config.Mode().IsRegular() {
			continue
		}
		modules = append(modules, entry.Name())
	}
	sort.Strings(modules)

	return modules, nil
}

// 创建一批不允许覆盖的 Go 文件
func (scaffold *Scaffold) CreateCode(codes []CodeFile) error {
	if scaffold == nil {
		return exception.Core("代码脚手架未初始化")
	}
	scaffold.mu.Lock()
	defer scaffold.mu.Unlock()

	return scaffold.createGoFiles(codes)
}

func (scaffold *Scaffold) createGoFiles(codes []CodeFile) error {
	root, err := scaffold.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()

	prepared, err := prepareCodeFiles(codes)
	if err != nil {
		return err
	}
	for _, file := range prepared {
		if err = validateCodeTarget(root, file.path); err != nil {
			return err
		}
	}

	created := make([]string, 0, len(prepared))
	for _, file := range prepared {
		if err = publishCodeFile(root, file); err != nil {
			for index := len(created) - 1; index >= 0; index-- {
				_ = root.Remove(created[index])
			}
			return err
		}
		created = append(created, file.path)
	}

	return nil
}

func (scaffold *Scaffold) openRoot() (*os.Root, error) {
	if scaffold == nil || scaffold.workspace == "" {
		return nil, exception.Core("代码脚手架未初始化")
	}
	root, err := os.OpenRoot(scaffold.workspace)
	if err != nil {
		return nil, exception.WrapCore(err, "打开代码工作区失败")
	}

	return root, nil
}

func prepareCodeFiles(codes []CodeFile) ([]preparedCodeFile, error) {
	prepared := make([]preparedCodeFile, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		clean, err := validateCodePath(code.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[clean]; exists {
			return nil, exception.Validate("代码目标路径重复: " + clean)
		}
		seen[clean] = struct{}{}
		formatted, err := format.Source([]byte(code.Content))
		if err != nil {
			return nil, exception.WrapValidate(err, "Go 源码语法无效: "+clean)
		}
		prepared = append(prepared, preparedCodeFile{path: clean, content: formatted})
	}

	return prepared, nil
}

func validateCodePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return "", exception.Validate("代码目标路径无效")
	}
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" || path.Clean(value) != value {
		return "", exception.Validate("代码目标路径必须是规范相对路径")
	}
	parts := strings.Split(value, "/")
	if len(parts) < 3 || parts[0] != "modules" {
		return "", exception.Validate("代码目标必须位于 modules 的模块目录内")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", exception.Validate("代码目标路径包含非法目录")
		}
	}
	if path.Ext(value) != ".go" {
		return "", exception.Validate("代码目标必须是 .go 文件")
	}

	return value, nil
}

func validateCodeTarget(root *os.Root, target string) error {
	parts := strings.Split(target, "/")
	for index := 1; index < len(parts); index++ {
		current := strings.Join(parts[:index], "/")
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return exception.WrapValidate(err, "检查代码目标目录失败")
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return exception.Validate("代码目标路径不允许符号链接: " + current)
		}
		if !info.IsDir() {
			return exception.Validate("代码目标父路径不是目录: " + current)
		}
	}
	info, err := root.Lstat(target)
	if err == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			return exception.Validate("代码目标不允许符号链接: " + target)
		}
		return exception.Validate("代码目标已存在: " + target)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return exception.WrapValidate(err, "检查代码目标失败")
	}

	return nil
}

func publishCodeFile(root *os.Root, file preparedCodeFile) error {
	directory := path.Dir(file.path)
	if err := root.MkdirAll(directory, 0o755); err != nil {
		return exception.WrapCore(err, "创建代码目录失败")
	}
	if err := validateCodeParents(root, file.path); err != nil {
		return err
	}
	temporary, handle, err := createTempCode(root, directory, path.Base(file.path))
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if _, err = handle.Write(file.content); err != nil {
		_ = handle.Close()
		return exception.WrapCore(err, "写入临时代码文件失败")
	}
	if err = handle.Sync(); err != nil {
		_ = handle.Close()
		return exception.WrapCore(err, "同步临时代码文件失败")
	}
	if err = handle.Close(); err != nil {
		return exception.WrapCore(err, "关闭临时代码文件失败")
	}
	if err = root.Link(temporary, file.path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exception.Validate("代码目标已存在: " + file.path)
		}
		return exception.WrapCore(err, "发布代码文件失败")
	}
	if err = root.Remove(temporary); err != nil {
		_ = root.Remove(file.path)
		return exception.WrapCore(err, "清理临时代码文件失败")
	}
	removeTemporary = false

	return nil
}

func validateCodeParents(root *os.Root, target string) error {
	parts := strings.Split(path.Dir(target), "/")
	for index := range parts {
		current := strings.Join(parts[:index+1], "/")
		info, err := root.Lstat(current)
		if err != nil {
			return exception.WrapValidate(err, "检查代码目标目录失败")
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return exception.Validate("代码目标目录无效: " + current)
		}
	}

	return nil
}

func createTempCode(root *os.Root, directory, name string) (string, *os.File, error) {
	for range 10 {
		random := make([]byte, 8)
		rand.Read(random)
		temporary := path.Join(directory, "."+name+".cool-"+hex.EncodeToString(random)+".tmp")
		handle, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return temporary, handle, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, exception.WrapCore(err, "创建临时代码文件失败")
		}
	}

	return "", nil, exception.Core("无法分配临时代码文件")
}
