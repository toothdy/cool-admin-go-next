package codegen

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

var codeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const (
	gMetaImportPath  = "github.com/gogf/gf/v2/frame/g"
	entityImportPath = "github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	gnctrlImportPath = "github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
)

// 开发端"生成代码"向导使用的实体字段元数据
type MenuColumn struct {
	PropertyName string `json:"propertyName"`
	Type         string `json:"type"`
	Length       string `json:"length"`
	Comment      string `json:"comment"`
	Nullable     bool   `json:"nullable"`
}

// 静态解析实体与 Controller 源码的结果
type MenuParseResult struct {
	Columns   []MenuColumn `json:"columns"`
	ClassName string       `json:"className,omitempty"`
	TableName string       `json:"tableName,omitempty"`
	FileName  string       `json:"fileName,omitempty"`
	Path      string       `json:"path"`
}

// 开发端模块代码创建请求
type MenuCreateInput struct {
	Module     string `json:"module"`
	Entity     string `json:"entity"`
	Controller string `json:"controller"`
	Service    string `json:"service"`
	FileName   string `json:"fileName"`
}

type parsedEntity struct {
	className string
	tableName string
	columns   []MenuColumn
}

// 实体列与 Controller 路径，不编译或执行输入源码
func (scaffold *Scaffold) ParseMenu(entitySource, controllerSource, moduleName string) (MenuParseResult, error) {
	if !validCodeName(moduleName) {
		return MenuParseResult{}, exception.Validate("模块名称无效")
	}
	parsed, err := parseMenuEntity(entitySource)
	if err != nil {
		return MenuParseResult{}, err
	}
	fileName := tableFileName(parsed.tableName)
	result := MenuParseResult{Columns: parsed.columns}
	if strings.TrimSpace(controllerSource) == "" {
		result.ClassName = parsed.className
		result.TableName = parsed.tableName
		result.FileName = fileName
		result.Path = "/admin/" + moduleName + "/" + fileName

		return result, nil
	}
	controllerPath, err := parseAdminPath(controllerSource)
	if err != nil {
		return MenuParseResult{}, err
	}
	if controllerPath == "" {
		controllerPath = fileName
	}
	result.Path = "/admin/" + moduleName + "/" + strings.Trim(controllerPath, "/")

	return result, nil
}

// 实体、Controller、Service 和缺失的模块配置
func (scaffold *Scaffold) CreateMenuCode(input MenuCreateInput) error {
	if scaffold == nil {
		return exception.Core("代码脚手架未初始化")
	}
	if !validCodeName(input.Module) || !validCodeName(input.FileName) {
		return exception.Validate("模块名称或文件名称无效")
	}
	if err := checkSourcePackage(input.Entity, "entity", "实体"); err != nil {
		return err
	}
	if err := checkSourcePackage(input.Controller, "admin", "Controller"); err != nil {
		return err
	}
	if err := checkSourcePackage(input.Service, "service", "Service"); err != nil {
		return err
	}

	scaffold.mu.Lock()
	defer scaffold.mu.Unlock()
	codes := []CodeFile{
		{Path: path.Join("modules", input.Module, "entity", input.FileName+".go"), Content: input.Entity},
		{Path: path.Join("modules", input.Module, "controller", "admin", input.FileName+".go"), Content: input.Controller},
		{Path: path.Join("modules", input.Module, "service", input.FileName+".go"), Content: input.Service},
	}
	configPath := path.Join("modules", input.Module, "config.go")
	exists, err := scaffold.validExistingConfig(configPath, input.Module)
	if err != nil {
		return err
	}
	if !exists {
		codes = append(codes, CodeFile{Path: configPath, Content: moduleConfigSource(input.Module)})
	}

	return scaffold.createGoFiles(codes)
}

func (scaffold *Scaffold) validExistingConfig(target, packageName string) (bool, error) {
	root, err := scaffold.openRoot()
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, exception.WrapValidate(err, "检查模块配置失败")
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, exception.Validate("模块配置必须是普通文件")
	}
	content, err := root.ReadFile(target)
	if err != nil {
		return false, exception.WrapCore(err, "读取模块配置失败")
	}
	if err = checkSourcePackage(string(content), packageName, "模块配置"); err != nil {
		return false, err
	}

	return true, nil
}

func parseMenuEntity(source string) (parsedEntity, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "entity.go", source, parser.AllErrors)
	if err != nil {
		return parsedEntity{}, exception.WrapValidate(err, "实体 Go 源码语法无效")
	}
	imports := sourceImports(file)
	type candidate struct {
		name    string
		value   *ast.StructType
		meta    reflect.StructTag
		hasBase bool
	}
	candidates := make([]candidate, 0, 1)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			meta, hasMeta := embeddedTag(structure, imports, gMetaImportPath, "Meta")
			if !hasMeta {
				continue
			}
			_, hasBase := embeddedTag(structure, imports, entityImportPath, "Base")
			candidates = append(candidates, candidate{name: typeSpec.Name.Name, value: structure, meta: meta, hasBase: hasBase})
		}
	}
	if len(candidates) != 1 {
		return parsedEntity{}, exception.Validate("实体源码必须包含且只包含一个带 g.Meta 的 struct")
	}
	current := candidates[0]
	if !current.hasBase {
		return parsedEntity{}, exception.Validate("实体必须嵌入 gnentity.Base")
	}
	tableName := structTagOption(current.meta.Get("orm"), "table")
	if !validCodeName(tableName) {
		return parsedEntity{}, exception.Validate("实体 g.Meta 缺少 orm table")
	}
	columns := []MenuColumn{{PropertyName: "id", Type: "number", Comment: "ID"}}
	for _, field := range current.value.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		tag, err := parseASTStructTag(field.Tag)
		if err != nil {
			return parsedEntity{}, err
		}
		jsonName := strings.Split(tag.Get("json"), ",")[0]
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = lowerFirst(field.Names[0].Name)
		}
		typeName, nullable := menuColumnType(field.Type, tag.Get("cool"))
		if typeName == "" {
			return parsedEntity{}, exception.Validate("不支持的实体字段类型: " + field.Names[0].Name)
		}
		columns = append(columns, MenuColumn{
			PropertyName: jsonName,
			Type:         typeName,
			Length:       structTagOption(tag.Get("cool"), "size"),
			Comment:      tag.Get("description"),
			Nullable:     nullable,
		})
	}
	columns = append(columns,
		MenuColumn{PropertyName: "createTime", Type: "date", Comment: "创建时间", Nullable: true},
		MenuColumn{PropertyName: "updateTime", Type: "date", Comment: "更新时间", Nullable: true},
	)

	return parsedEntity{className: current.name, tableName: tableName, columns: columns}, nil
}

func embeddedTag(
	structure *ast.StructType,
	imports map[string]string,
	importPath string,
	name string,
) (reflect.StructTag, bool) {
	for _, field := range structure.Fields.List {
		if len(field.Names) != 0 || !isImportedSelector(field.Type, imports, importPath, name) {
			continue
		}
		tag, err := parseASTStructTag(field.Tag)
		if err == nil {
			return tag, true
		}
	}

	return "", false
}

func parseASTStructTag(tag *ast.BasicLit) (reflect.StructTag, error) {
	if tag == nil {
		return "", nil
	}
	value, err := strconv.Unquote(tag.Value)
	if err != nil {
		return "", exception.WrapValidate(err, "实体字段标签无效")
	}

	return reflect.StructTag(value), nil
}

func isImportedSelector(expression ast.Expr, imports map[string]string, importPath, name string) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)

	return ok && imports[qualifier.Name] == importPath
}

func menuColumnType(expression ast.Expr, coolTag string) (string, bool) {
	nullable := false
	if pointer, ok := expression.(*ast.StarExpr); ok {
		nullable = true
		expression = pointer.X
	}
	if structTagOption(coolTag, "json") == "true" {
		return "json", nullable
	}
	switch value := expression.(type) {
	case *ast.Ident:
		switch value.Name {
		case "string":
			return "string", nullable
		case "bool":
			return "boolean", nullable
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64":
			return "number", nullable
		}
	case *ast.SelectorExpr:
		if value.Sel.Name == "Time" {
			return "date", nullable
		}
	case *ast.ArrayType, *ast.MapType, *ast.InterfaceType, *ast.StructType:
		return "json", nullable
	}

	return "", nullable
}

func parseAdminPath(source string) (string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "controller.go", source, parser.AllErrors)
	if err != nil {
		return "", exception.WrapValidate(err, "Controller Go 源码语法无效")
	}
	imports := sourceImports(file)
	paths := make([]string, 0, 1)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Admin" {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok || imports[qualifier.Name] != gnctrlImportPath {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr == nil {
			paths = append(paths, value)
		}
		return true
	})
	if len(paths) != 1 {
		return "", exception.Validate("Controller 必须包含且只包含一个 controller.Admin 字符串声明")
	}
	value := strings.Trim(paths[0], "/")
	if value != "" {
		for _, segment := range strings.Split(value, "/") {
			if !validCodeName(segment) {
				return "", exception.Validate("Controller 路径无效")
			}
		}
	}

	return value, nil
}

func checkSourcePackage(source, expected, label string) error {
	file, err := parser.ParseFile(token.NewFileSet(), label+".go", source, parser.AllErrors)
	if err != nil {
		return exception.WrapValidate(err, label+" Go 源码语法无效")
	}
	if file.Name == nil || file.Name.Name != expected {
		return exception.Validate(label + " package 必须是 " + expected)
	}

	return nil
}

func moduleConfigSource(moduleName string) string {
	return `package ` + moduleName + `

import "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

type Config struct{}

func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "` + moduleName + `",
		Description: "` + moduleName + ` module",
	}
}
`
}

func tableFileName(tableName string) string {
	parts := strings.Split(tableName, "_")

	return parts[len(parts)-1]
}

func structTagOption(value, key string) string {
	for _, option := range strings.Split(value, ",") {
		name, result, found := strings.Cut(strings.TrimSpace(option), ":")
		if !found {
			name, result, found = strings.Cut(strings.TrimSpace(option), "=")
		}
		if found && name == key {
			return result
		}
	}

	return ""
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}

	return strings.ToLower(value[:1]) + value[1:]
}

func sourceImports(file *ast.File) map[string]string {
	result := make(map[string]string, len(file.Imports))
	for _, specification := range file.Imports {
		value, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(value)
		if specification.Name != nil {
			name = specification.Name.Name
		}
		if name != "_" && name != "." {
			result[name] = value
		}
	}

	return result
}

func validCodeName(value string) bool {
	return codeNamePattern.MatchString(value)
}
