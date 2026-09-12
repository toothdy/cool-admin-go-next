package route

import (
	"fmt"
	"go/token"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 请求绑定来源
type BindSource string

const (
	BindAuto  BindSource = "auto"  // 自动绑定
	BindJSON  BindSource = "json"  // JSON 请求体
	BindQuery BindSource = "query" // 查询参数
	BindForm  BindSource = "form"  // 表单参数
	BindPath  BindSource = "path"  // 路径参数
	BindFile  BindSource = "file"  // 上传文件
)

// 路由种类
type Kind string

const (
	KindCRUD   Kind = "crud"   // CRUD 路由
	KindCustom Kind = "custom" // 自定义路由
)

// 路由事务策略
type TransactionPolicy struct {
	nonTransactional bool
}

// 非事务策略
func NonTransactional() TransactionPolicy {
	return TransactionPolicy{nonTransactional: true}
}

// 是否显式关闭事务
func (policy TransactionPolicy) IsNonTransactional() bool {
	return policy.nonTransactional
}

// 生成期解析的可调用符号
type CallableRef struct {
	HasRequest         bool
	Method             string
	PackagePath        string
	RequestPackagePath string
	RequestType        string
	ReturnsValue       bool
	Symbol             string
	Type               string
}

// 静态 Controller 输入
type ControllerDefinition struct {
	Alias              []string
	Description        string
	DevelopmentOnly    bool
	Factory            CallableRef
	IgnoreGlobalPrefix bool
	Key                string
	Middleware         []string
	Module             string
	Path               string
	Sensitive          bool
	TagName            string
}

// 静态路由输入
type Definition struct {
	Bind            BindSource
	Controller      string
	Description     string
	DevelopmentOnly bool
	Handler         CallableRef
	Kind            Kind
	Method          string
	Middleware      []string
	Path            string
	Summary         string
	Tags            []string
	Transaction     TransactionPolicy
}

// 静态路由表输入
type TableInput struct {
	Controllers []ControllerDefinition
	Routes      []Definition
}

// 不可变静态路由表
type Table struct {
	controllers []Controller
	routes      []Route
}

// 已校验的 Controller 描述
type Controller struct {
	alias              []string
	description        string
	developmentOnly    bool
	factory            CallableRef
	ignoreGlobalPrefix bool
	key                string
	middleware         []string
	module             string
	path               string
	sensitive          bool
	tagName            string
}

// 已校验的静态路由
type Route struct {
	bind            BindSource
	controller      string
	description     string
	developmentOnly bool
	handler         CallableRef
	kind            Kind
	method          string
	middleware      []string
	path            string
	summary         string
	tags            []string
	transaction     TransactionPolicy
}

// 不可变静态路由表
func BuildTable(input TableInput) (Table, error) {
	controllers, controllerKeys, err := compileControllers(input.Controllers)
	if err != nil {
		return Table{}, err
	}
	routes, err := compileRoutes(input.Routes, controllerKeys)
	if err != nil {
		return Table{}, err
	}

	return Table{controllers: controllers, routes: routes}, nil
}

// Controller 副本
func (table Table) Controllers() []Controller {
	result := append([]Controller(nil), table.controllers...)
	for index := range result {
		result[index].alias = append([]string(nil), result[index].alias...)
		result[index].middleware = append([]string(nil), result[index].middleware...)
	}

	return result
}

// 路由副本
func (table Table) Routes() []Route {
	result := append([]Route(nil), table.routes...)
	for index := range result {
		result[index].middleware = append([]string(nil), result[index].middleware...)
		result[index].tags = append([]string(nil), result[index].tags...)
	}

	return result
}

// Controller 唯一键
func (controller Controller) Key() string { return controller.key }

// 所属模块
func (controller Controller) Module() string { return controller.module }

// Controller 完整路径
func (controller Controller) Path() string { return controller.path }

// Controller 别名副本
func (controller Controller) Alias() []string { return append([]string(nil), controller.alias...) }

// Controller 中间件副本
func (controller Controller) Middleware() []string {
	return append([]string(nil), controller.middleware...)
}

// Controller 描述
func (controller Controller) Description() string { return controller.description }

// 是否仅在开发环境注册
func (controller Controller) DevelopmentOnly() bool { return controller.developmentOnly }

// Controller 标签名
func (controller Controller) TagName() string { return controller.tagName }

// 路径是否大小写敏感
func (controller Controller) Sensitive() bool { return controller.sensitive }

// 是否忽略全局前缀
func (controller Controller) IgnoreGlobalPrefix() bool { return controller.ignoreGlobalPrefix }

// Controller 工厂引用
func (controller Controller) Factory() CallableRef { return controller.factory }

// 所属 Controller 唯一键
func (route Route) Controller() string { return route.controller }

// 路由种类
func (route Route) Kind() Kind { return route.kind }

// 规范化 HTTP Method
func (route Route) Method() string { return route.method }

// 规范化完整路径
func (route Route) Path() string { return route.path }

// 路由摘要
func (route Route) Summary() string { return route.summary }

// 路由描述
func (route Route) Description() string { return route.description }

// 是否仅在开发环境注册
func (route Route) DevelopmentOnly() bool { return route.developmentOnly }

// 处理器引用
func (route Route) Handler() CallableRef { return route.handler }

// 已解析绑定来源
func (route Route) Bind() BindSource { return route.bind }

// 路由中间件副本
func (route Route) Middleware() []string { return append([]string(nil), route.middleware...) }

// 路由标签副本
func (route Route) Tags() []string { return append([]string(nil), route.tags...) }

// 事务策略
func (route Route) Transaction() TransactionPolicy { return route.transaction }

func compileControllers(definitions []ControllerDefinition) ([]Controller, map[string]bool, error) {
	result := make([]Controller, len(definitions))
	keys := make(map[string]bool, len(definitions))
	aliases := make(map[string]string)
	for index, definition := range definitions {
		current, err := compileController(definition)
		if err != nil {
			return nil, nil, err
		}
		if keys[current.key] {
			return nil, nil, exception.Core(fmt.Sprintf("Controller 重复: %s", current.key))
		}
		for _, alias := range current.alias {
			if owner, exists := aliases[alias]; exists {
				return nil, nil, exception.Core(fmt.Sprintf("Controller Alias %s 与 %s 冲突", alias, owner))
			}
			aliases[alias] = current.key
		}
		keys[current.key] = true
		result[index] = current
	}

	return result, keys, nil
}

func compileController(definition ControllerDefinition) (Controller, error) {
	if strings.TrimSpace(definition.Key) == "" {
		return Controller{}, exception.Core("Controller 唯一键不能为空")
	}
	if !validModule(definition.Module) {
		return Controller{}, exception.Core(fmt.Sprintf("Controller %s 模块无效", definition.Key))
	}
	fullPath, err := normalizePath(definition.Path)
	if err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 路径无效", definition.Key))
	}
	aliases, err := validateValues("Alias", definition.Alias, validAlias)
	if err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 无效", definition.Key))
	}
	middleware, err := validateValues("中间件", definition.Middleware, validSymbolPath)
	if err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 无效", definition.Key))
	}
	if err = validateCallable(definition.Factory, false); err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 工厂无效", definition.Key))
	}
	if err = validateText("描述", definition.Description, true); err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 无效", definition.Key))
	}
	if err = validateText("标签名", definition.TagName, true); err != nil {
		return Controller{}, exception.WrapCore(err, fmt.Sprintf("Controller %s 无效", definition.Key))
	}

	return Controller{
		alias:              aliases,
		description:        definition.Description,
		developmentOnly:    definition.DevelopmentOnly,
		factory:            definition.Factory,
		ignoreGlobalPrefix: definition.IgnoreGlobalPrefix,
		key:                definition.Key,
		middleware:         middleware,
		module:             definition.Module,
		path:               fullPath,
		sensitive:          definition.Sensitive,
		tagName:            definition.TagName,
	}, nil
}

func compileRoutes(definitions []Definition, controllerKeys map[string]bool) ([]Route, error) {
	result := make([]Route, len(definitions))
	keys := make(map[string]bool, len(definitions))
	for index, definition := range definitions {
		current, err := compileRoute(definition, controllerKeys)
		if err != nil {
			return nil, err
		}
		key := current.method + " " + current.path
		if keys[key] {
			return nil, exception.Core(fmt.Sprintf("路由冲突: %s", key))
		}
		keys[key] = true
		result[index] = current
	}

	return result, nil
}

func compileRoute(definition Definition, controllerKeys map[string]bool) (Route, error) {
	if !controllerKeys[definition.Controller] {
		return Route{}, exception.Core(fmt.Sprintf("路由所属 Controller 不存在: %s", definition.Controller))
	}
	method, err := normalizeMethod(definition.Method)
	if err != nil {
		return Route{}, err
	}
	fullPath, err := normalizePath(definition.Path)
	if err != nil {
		return Route{}, err
	}
	if definition.Kind != KindCRUD && definition.Kind != KindCustom {
		return Route{}, exception.Core(fmt.Sprintf("路由 %s %s 种类无效", method, fullPath))
	}
	if definition.Bind == BindAuto || !validBind(definition.Bind) {
		return Route{}, exception.Core(fmt.Sprintf("路由 %s %s 绑定来源未解析", method, fullPath))
	}
	if definition.Kind == KindCRUD && definition.Transaction.IsNonTransactional() {
		return Route{}, exception.Core(fmt.Sprintf("默认 CRUD 路由 %s %s 不能关闭事务", method, fullPath))
	}
	if err = validateCallable(definition.Handler, true); err != nil {
		return Route{}, exception.WrapCore(err, fmt.Sprintf("路由 %s %s Handler 无效", method, fullPath))
	}
	middleware, err := validateValues("中间件", definition.Middleware, validSymbolPath)
	if err != nil {
		return Route{}, exception.WrapCore(err, fmt.Sprintf("路由 %s %s 无效", method, fullPath))
	}
	tags, err := validateValues("标签", definition.Tags, validTag)
	if err != nil {
		return Route{}, exception.WrapCore(err, fmt.Sprintf("路由 %s %s 无效", method, fullPath))
	}
	if err = validateText("摘要", definition.Summary, true); err != nil {
		return Route{}, exception.WrapCore(err, fmt.Sprintf("路由 %s %s 无效", method, fullPath))
	}
	if err = validateText("描述", definition.Description, true); err != nil {
		return Route{}, exception.WrapCore(err, fmt.Sprintf("路由 %s %s 无效", method, fullPath))
	}

	return Route{
		bind:            definition.Bind,
		controller:      definition.Controller,
		description:     definition.Description,
		developmentOnly: definition.DevelopmentOnly,
		handler:         definition.Handler,
		kind:            definition.Kind,
		method:          method,
		middleware:      middleware,
		path:            fullPath,
		summary:         definition.Summary,
		tags:            tags,
		transaction:     definition.Transaction,
	}, nil
}

func normalizeMethod(value string) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(value))
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return method, nil
	default:
		return "", exception.Core(fmt.Sprintf("HTTP Method %q 无效", value))
	}
}

func normalizePath(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") {
		return "", exception.Core(fmt.Sprintf("完整路径 %q 无效", value))
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", exception.Core(fmt.Sprintf("完整路径 %q 无效", value))
		}
	}
	normalized := path.Clean(value)
	if normalized != value && !(strings.HasSuffix(value, "/") && strings.TrimSuffix(value, "/") == normalized) {
		return "", exception.Core(fmt.Sprintf("完整路径 %q 未规范化", value))
	}

	return normalized, nil
}

func validBind(value BindSource) bool {
	switch value {
	case BindJSON, BindQuery, BindForm, BindPath, BindFile:
		return true
	default:
		return false
	}
}

func validateCallable(reference CallableRef, requiresMethod bool) error {
	if reference.PackagePath == "" || strings.Trim(reference.PackagePath, "/") != reference.PackagePath || strings.Contains(reference.PackagePath, "//") {
		return exception.Core("包路径无效")
	}
	if !token.IsIdentifier(reference.Symbol) {
		return exception.Core("符号名称无效")
	}
	if requiresMethod && !token.IsIdentifier(reference.Method) {
		return exception.Core("方法名称无效")
	}
	if strings.TrimSpace(reference.Type) == "" {
		return exception.Core("类型不能为空")
	}
	if reference.HasRequest {
		if reference.RequestPackagePath == "" || strings.Trim(reference.RequestPackagePath, "/") != reference.RequestPackagePath ||
			strings.Contains(reference.RequestPackagePath, "//") || !token.IsIdentifier(reference.RequestType) {
			return exception.Core("请求类型无效")
		}
	} else if reference.RequestPackagePath != "" || reference.RequestType != "" {
		return exception.Core("无请求 Handler 不能声明请求类型")
	}

	return nil
}

func validateValues(label string, values []string, validate func(string) bool) ([]string, error) {
	result := append([]string(nil), values...)
	seen := make(map[string]bool, len(result))
	for _, value := range result {
		if !validate(value) {
			return nil, exception.Core(fmt.Sprintf("%s %q 无效", label, value))
		}
		if seen[value] {
			return nil, exception.Core(fmt.Sprintf("%s %q 重复", label, value))
		}
		seen[value] = true
	}

	return result, nil
}

func validAlias(value string) bool {
	return strings.TrimSpace(value) == value && token.IsIdentifier(value)
}

func validModule(value string) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !token.IsIdentifier(segment) {
			return false
		}
	}

	return true
}

func validSymbolPath(value string) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if !token.IsIdentifier(segment) {
			return false
		}
	}

	return true
}

func validTag(value string) bool {
	return strings.TrimSpace(value) == value && token.IsIdentifier(value)
}

func validateText(label, value string, optional bool) error {
	if value == "" && optional {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return exception.Core(fmt.Sprintf("%s不能为空", label))
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return exception.Core(fmt.Sprintf("%s不能包含控制字符", label))
		}
	}

	return nil
}

// 校验 Controller 前缀等相对路径
func ValidRelativePath(value string) bool {
	if value == "" {
		return true
	}
	if strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.ContainsAny(value, "?#") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}

	return true
}
