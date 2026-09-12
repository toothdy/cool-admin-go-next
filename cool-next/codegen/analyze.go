package codegen

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
)

// 分析工作区选项
type Options struct {
	Dir         string // Go module 根目录
	ModulesRoot string // 相对模块根目录
}

type analysis struct {
	diagnostics []Diagnostic
	dir         string
	eligible    map[string]bool
	modulesRoot string
	packages    *packageIndex
}

// 分析模块源码
func Analyze(ctx context.Context, options Options) (*Model, error) {
	return analyzeOverlay(ctx, options, nil)
}

// 使用受控源码覆盖分析模块源码
func analyzeOverlay(ctx context.Context, options Options, overlay map[string][]byte) (*Model, error) {
	overlay = cloneOverlay(overlay)
	dir, modulesRoot, err := checkOptions(options)
	if err != nil {
		return nil, &DiagnosticError{diagnostics: []Diagnostic{{Code: "CG001", Message: err.Error()}}}
	}
	roots, diagnostics := moduleRoots(dir, modulesRoot)
	if len(diagnostics) > 0 {
		sortDiagnostics(diagnostics)
		return nil, &DiagnosticError{diagnostics: diagnostics}
	}
	if len(roots) == 0 {
		return &Model{}, nil
	}
	if diagnostics = checkDirs(dir, roots); len(diagnostics) > 0 {
		sortDiagnostics(diagnostics)
		return nil, &DiagnosticError{diagnostics: diagnostics}
	}
	eligible := eligibleFiles(roots)
	packages, diagnostics := loadPackages(ctx, dir, modulesRoot, overlay)
	if len(diagnostics) > 0 {
		sortDiagnostics(diagnostics)
		return nil, &DiagnosticError{diagnostics: diagnostics}
	}
	current := &analysis{dir: dir, eligible: eligible, modulesRoot: modulesRoot, packages: packages}
	model := &Model{}
	for _, root := range roots {
		current.analyzeModule(root, model)
	}
	current.checkConsumerNames(model)
	current.checkRouteConflicts(model)
	sortDiagnostics(current.diagnostics)
	if len(current.diagnostics) > 0 {
		return nil, &DiagnosticError{diagnostics: current.diagnostics}
	}
	sort.Slice(model.modules, func(left, right int) bool {
		return model.modules[left].identity.Key() < model.modules[right].identity.Key()
	})
	return model, nil
}

func cloneOverlay(source map[string][]byte) map[string][]byte {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(source))
	for path, content := range source {
		cloned[path] = append([]byte(nil), content...)
	}
	return cloned
}

func checkOptions(options Options) (string, string, error) {
	if options.Dir == "" {
		return "", "", fmt.Errorf("工作区目录不能为空")
	}
	dir, err := filepath.Abs(options.Dir)
	if err != nil {
		return "", "", fmt.Errorf("工作区目录无效: %w", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("工作区目录不存在")
	}
	if options.ModulesRoot == "" || filepath.IsAbs(options.ModulesRoot) || filepath.Clean(options.ModulesRoot) != options.ModulesRoot || strings.HasPrefix(options.ModulesRoot, "..") {
		return "", "", fmt.Errorf("模块根目录必须是干净相对路径")
	}
	return dir, filepath.Join(dir, options.ModulesRoot), nil
}

func moduleRoots(dir, modulesRoot string) ([]string, []Diagnostic) {
	if _, err := os.Stat(modulesRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []Diagnostic{{Code: "CG002", Message: "读取模块根目录失败"}}
	}
	var candidates []string
	err := filepath.WalkDir(modulesRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != modulesRoot && entry.IsDir() && ignoredDir(entry.Name()) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "config.go" && !isGeneratedFile(path) {
			candidates = append(candidates, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, []Diagnostic{{Code: "CG002", Message: "扫描模块目录失败"}}
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftDepth := len(strings.Split(filepath.ToSlash(candidates[left]), "/"))
		rightDepth := len(strings.Split(filepath.ToSlash(candidates[right]), "/"))
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return candidates[left] < candidates[right]
	})
	var roots []string
	var diagnostics []Diagnostic
	for _, candidate := range candidates {
		ancestor := nearestRoot(roots, candidate)
		if ancestor != "" {
			relative, _ := filepath.Rel(ancestor, candidate)
			if allowedDir(relative) && !hasModuleConfig(filepath.Join(candidate, "config.go")) {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{Code: "CG003", Message: "模块根目录不能重叠", Position: positionFromPath(dir, filepath.Join(candidate, "config.go"))})
		}
		roots = append(roots, candidate)
	}
	sort.Strings(roots)
	return roots, diagnostics
}

func hasModuleConfig(path string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == "ModuleConfig" {
			return true
		}
	}
	return false
}

func nearestRoot(roots []string, candidate string) string {
	nearest := ""
	for _, root := range roots {
		relative, err := filepath.Rel(root, candidate)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(nearest) {
			nearest = root
		}
	}
	return nearest
}

func eligibleFiles(roots []string) map[string]bool {
	files := make(map[string]bool)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if path != root && entry.IsDir() && ignoredDir(entry.Name()) {
				return filepath.SkipDir
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || isGeneratedFile(path) {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			if filepath.Base(relative) == "config.go" || allowedDir(filepath.Dir(relative)) {
				files[path] = true
			}
			return nil
		})
	}
	return files
}

// 模块目录协议 (v2) 允许的顶级子目录，见 README「模块目录协议」
var moduleDirs = []string{
	"contract", "entity", "service", "controller", "middleware", "event", "schedule", "queue", "consumer", "dto", "grpc",
}

func allowedDir(directory string) bool {
	if directory == "." {
		return false
	}
	first := strings.Split(filepath.ToSlash(directory), "/")[0]
	for _, allowed := range moduleDirs {
		if first == allowed {
			return true
		}
	}
	return false
}

func ignoredDir(name string) bool { return name == "testdata" || strings.HasPrefix(name, ".") }

// 校验模块根目录并显式报告协议外内容
func checkDirs(dir string, roots []string) []Diagnostic {
	var diagnostics []Diagnostic
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if ignoredDir(name) {
				continue
			}
			if entry.IsDir() {
				if !allowedDir(name) {
					diagnostics = append(diagnostics, Diagnostic{
						Code:     "CG111",
						Message:  fmt.Sprintf("模块目录 %q 不在允许列表内，见 README 模块目录协议", name),
						Position: positionFromPath(dir, filepath.Join(root, name)),
					})
				}
				continue
			}
			if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if name != "config.go" {
				diagnostics = append(diagnostics, Diagnostic{
					Code:     "CG111",
					Message:  fmt.Sprintf("模块根目录只能包含 config.go，%q 必须放入协议子目录", name),
					Position: positionFromPath(dir, filepath.Join(root, name)),
				})
			}
		}
	}
	return diagnostics
}

func isGeneratedFile(path string) bool {
	name := filepath.Base(path)
	if strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, "_grpc.pb.go") || strings.HasSuffix(name, "_gen.go") {
		return true
	}
	content, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(content), "Code generated")
}

func (a *analysis) analyzeModule(root string, model *Model) {
	modulesRoot, _ := filepath.Rel(a.dir, a.modulesRoot)
	directory, _ := filepath.Rel(a.dir, root)
	identity, err := module.NewIdentity(modulesRoot, directory)
	if err != nil {
		a.add("CG004", "模块身份无效", positionFromPath(a.dir, filepath.Join(root, "config.go")))
		return
	}
	configFile := filepath.Join(root, "config.go")
	pkg := a.packages.byFile[configFile]
	if pkg == nil {
		a.add("CG005", "无法加载模块根包", positionFromPath(a.dir, configFile))
		return
	}
	a.analyzeQueryDSL(root)
	result := Module{identity: identity, root: filepath.ToSlash(directory)}
	result.seedDB = fileExists(filepath.Join(root, "db.json"))
	result.seedMenu = fileExists(filepath.Join(root, "menu.json"))
	result.config, result.references = a.analyzeConfig(pkg, configFile, root)
	result.entities, result.schemas = a.analyzeEntities(root)
	result.constructors = a.analyzeConstructors(root)
	result.registrars = a.analyzeGRPC(root)
	result.services = a.analyzeServices(root)
	result.controllers = a.analyzeControllers(root, identity, result.entities, result.services)
	model.modules = append(model.modules, result)
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func (a *analysis) add(code, message string, position Position) {
	a.diagnostics = append(a.diagnostics, Diagnostic{Code: code, Message: message, Position: position})
}

func positionFromPath(root, path string) Position {
	relative, _ := filepath.Rel(root, path)
	return Position{File: filepath.ToSlash(relative), Line: 1, Column: 1}
}
