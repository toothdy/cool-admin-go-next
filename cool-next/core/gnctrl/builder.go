package gnctrl

import (
	"fmt"
	"go/token"
	"net/http"
	"reflect"
	"strings"
	"unicode"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/route"
)

// 不可变 Controller 定义
type Definition interface {
	definition()
}

// 定义构造器
type Builder interface {
	Options(RouterOptions) Builder
	Curd(CurdOption) Builder
	Route(...Route) Builder
	Build() Definition
}

// 静态中间件引用
type MiddlewareRef = module.ComponentRef

// 请求绑定来源
type BindSource = route.BindSource

const (
	BindAuto  = route.BindAuto  // 自动绑定
	BindJSON  = route.BindJSON  // JSON 请求体
	BindQuery = route.BindQuery // 查询参数
	BindForm  = route.BindForm  // 表单参数
	BindPath  = route.BindPath  // 路径参数
	BindFile  = route.BindFile  // 上传文件
)

// 路由事务策略
type TransactionPolicy = route.TransactionPolicy

// 路由选项
type RouterOptions struct {
	Sensitive          *bool
	Middleware         []MiddlewareRef
	Alias              []string
	Description        string
	TagName            string
	IgnoreGlobalPrefix bool
	DevelopmentOnly    bool
}

// 受控自定义路由处理器
type Handler struct {
	value any
}

// 自定义路由声明
type Route struct {
	Method             string
	Path               string
	Summary            string
	Description        string
	DevelopmentOnly    bool
	Handler            Handler
	Bind               BindSource
	Middleware         []MiddlewareRef
	Tags               []URLTag
	Transaction        TransactionPolicy
	IgnoreGlobalPrefix bool
}

// 默认 CRUD API
type APIType string

const (
	Add    APIType = "add"    // 新增
	Delete APIType = "delete" // 删除
	Update APIType = "update" // 更新
	Info   APIType = "info"   // 详情
	List   APIType = "list"   // 列表
	Page   APIType = "page"   // 分页

	TagIgnoreToken = "ignoreToken" // 忽略 Token 路由标签
)

// 默认 CRUD 路由标签
type URLTag struct {
	Name string
	URL  []APIType
}

// 默认 CRUD 配置
type CurdOption struct {
	Prefix             string
	API                []APIType
	PageQueryOp        QueryProvider
	ListQueryOp        QueryProvider
	InsertParam        InsertParam
	Before             BeforeFunc
	InfoIgnoreProperty []ColumnRef
	Entity             any
	Service            any
	URLTag             *URLTag

	HiddenFields   []ColumnRef
	ReadonlyFields []ColumnRef
	SortFields     []ColumnRef
	DefaultSort    ColumnRef
	DefaultOrder   Direction
}

// Controller 所属区域
type Area string

const (
	AreaAdmin Area = "admin" // 后台区域
	AreaApp   Area = "app"   // 应用端区域
)

type builder struct {
	area       Area
	curd       *CurdOption
	hasCurd    bool
	hasOptions bool
	options    RouterOptions
	path       string
	routes     []Route
}

type definition struct {
	area    Area
	curd    *CurdOption
	options RouterOptions
	path    string
	routes  []Route
}

func (*definition) definition() {}

// 创建后台 Controller Builder
func Admin(path ...string) Builder {
	return newBuilder(AreaAdmin, pathArg(path))
}

// 创建应用端 Controller Builder
func App(path ...string) Builder {
	return newBuilder(AreaApp, pathArg(path))
}

func pathArg(path []string) string {
	if len(path) > 1 {
		panicCore("Controller 路径最多只能传一个")
	}
	if len(path) == 0 {
		return ""
	}

	return path[0]
}

// 创建可区分未配置状态的布尔值
func Bool(value bool) *bool {
	result := value

	return &result
}

// 创建自定义路由处理器
func Handle(handler any) Handler {
	if isNilValue(handler) || reflect.TypeOf(handler).Kind() != reflect.Func {
		panicCore("Route Handler 必须是非 nil 函数")
	}

	return Handler{value: handler}
}

// 创建非事务路由策略
func NonTransactional() TransactionPolicy {
	return route.NonTransactional()
}

// CRUD API 列表副本
func API(values ...APIType) []APIType {
	result := append([]APIType(nil), values...)
	checkAPIs(result)

	return result
}

// 全部默认 CRUD API
func AllAPI() []APIType {
	return API(Add, Delete, Update, Info, List, Page)
}

// 配置 Controller 路由选项
func (current *builder) Options(options RouterOptions) Builder {
	if current == nil {
		panicCore("Controller Builder 不能为空")
	}
	if current.hasOptions {
		panicCore("Controller 不能重复声明 Options")
	}
	checkOptions(options)
	current.options = cloneOptions(options)
	current.hasOptions = true

	return current
}

// 配置默认 CRUD
func (current *builder) Curd(option CurdOption) Builder {
	if current == nil {
		panicCore("Controller Builder 不能为空")
	}
	if current.hasCurd {
		panicCore("Controller 不能重复声明 Curd")
	}
	checkCurd(option)
	cloned := cloneCurdOption(option)
	current.curd = &cloned
	current.hasCurd = true

	return current
}

// 添加自定义路由
func (current *builder) Route(routes ...Route) Builder {
	if current == nil {
		panicCore("Controller Builder 不能为空")
	}
	for _, route := range routes {
		checkRoute(route)
		current.routes = append(current.routes, cloneRoute(route))
	}

	return current
}

// 构建不可变 Controller 定义
func (current *builder) Build() Definition {
	if current == nil {
		panicCore("Controller Builder 不能为空")
	}
	result := &definition{
		area:    current.area,
		options: cloneOptions(current.options),
		path:    current.path,
		routes:  cloneRoutes(current.routes),
	}
	if current.curd != nil {
		cloned := cloneCurdOption(*current.curd)
		result.curd = &cloned
	}

	return result
}

func newBuilder(controllerArea Area, path string) Builder {
	checkRelPath(path, "Controller 路径")

	return &builder{area: controllerArea, path: path}
}

func checkOptions(options RouterOptions) {
	if options.Sensitive != nil {
		_ = *options.Sensitive
	}
	checkMiddleware(options.Middleware, "Controller Middleware")
	checkAliases(options.Alias)
	checkText(options.Description, "Controller Description")
	checkText(options.TagName, "Controller TagName")
}

func checkRoute(value Route) {
	checkMethod(value.Method)
	checkRoutePath(value.Path)
	checkText(value.Summary, "Route Summary")
	checkText(value.Description, "Route Description")
	if isNilValue(value.Handler.value) || reflect.TypeOf(value.Handler.value).Kind() != reflect.Func {
		panicCore("Route Handler 无效")
	}
	if value.Bind != "" && value.Bind != BindAuto && value.Bind != BindJSON && value.Bind != BindQuery && value.Bind != BindForm && value.Bind != BindPath && value.Bind != BindFile {
		panicCore("Route Bind %q 无效", value.Bind)
	}
	checkMiddleware(value.Middleware, "Route Middleware")
	seenTags := make(map[string]bool, len(value.Tags))
	for _, tag := range value.Tags {
		if len(tag.URL) > 0 {
			panicCore("自定义 Route 标签不能选择 CRUD API")
		}
		checkURLTag(tag, nil)
		if seenTags[tag.Name] {
			panicCore("Route 标签 %s 重复", tag.Name)
		}
		seenTags[tag.Name] = true
	}
}

func checkCurd(option CurdOption) {
	checkRelPath(option.Prefix, "Curd Prefix")
	checkAPIs(option.API)
	checkEntity(option.Entity)
	checkService(option.Service)
	if option.PageQueryOp != nil {
		requireProvider(option.PageQueryOp)
	}
	if option.ListQueryOp != nil {
		requireProvider(option.ListQueryOp)
	}
	if option.InsertParam != nil {
		requireInsertParam(option.InsertParam)
	}
	if option.URLTag != nil {
		checkURLTag(*option.URLTag, option.API)
	}
}

func checkRelPath(path, label string) {
	if !route.ValidRelativePath(path) {
		panicCore("%s无效", label)
	}
}

func checkRoutePath(value string) {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.ContainsAny(value, "?#") {
		panicCore("Route Path 无效")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			panicCore("Route Path 无效")
		}
	}
}

func checkMethod(value string) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return
	default:
		panicCore("Route Method %q 无效", value)
	}
}

func checkAliases(values []string) {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !token.IsIdentifier(value) {
			panicCore("Controller Alias %q 无效", value)
		}
		if seen[value] {
			panicCore("Controller Alias %s 重复", value)
		}
		seen[value] = true
	}
}

func checkMiddleware(values []MiddlewareRef, label string) {
	seen := make(map[MiddlewareRef]bool, len(values))
	for _, value := range values {
		if !validComponentRef(value) {
			panicCore("%s %q 无效", label, value)
		}
		if seen[value] {
			panicCore("%s %q 重复", label, value)
		}
		seen[value] = true
	}
}

func validComponentRef(value MiddlewareRef) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(string(value), ".") {
		if !token.IsIdentifier(segment) {
			return false
		}
	}

	return true
}

func checkText(value, label string) {
	if value == "" {
		return
	}
	if strings.TrimSpace(value) == "" {
		panicCore("%s 不能为空", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			panicCore("%s 不能包含控制字符", label)
		}
	}
}

func checkAPIs(values []APIType) {
	seen := make(map[APIType]struct{}, len(values))
	for _, value := range values {
		if !isAPI(value) {
			panicCore("CRUD API %q 无效", value)
		}
		if _, exists := seen[value]; exists {
			panicCore("CRUD API %s 重复", value)
		}
		seen[value] = struct{}{}
	}
}

func checkURLTag(tag URLTag, enabled []APIType) {
	if strings.TrimSpace(tag.Name) == "" {
		panicCore("URLTag 名称不能为空")
	}
	checkAPIs(tag.URL)
	if len(tag.URL) == 0 {
		return
	}
	available := make(map[APIType]struct{}, len(enabled))
	for _, value := range enabled {
		available[value] = struct{}{}
	}
	for _, value := range tag.URL {
		if _, exists := available[value]; !exists {
			panicCore("URLTag API %s 未在 Curd 中启用", value)
		}
	}
}

func checkEntity(value any) {
	valueType := reflect.TypeOf(value)
	if valueType == nil || valueType.Kind() != reflect.Struct || valueType.Name() == "" || valueType.PkgPath() == "" || !reflect.ValueOf(value).IsZero() {
		panicCore("Curd Entity 必须是非指针具名 struct 零值")
	}
}

func checkService(value any) {
	if isNilValue(value) || reflect.TypeOf(value).Kind() != reflect.Pointer {
		panicCore("Curd Service 必须是非 nil 指针")
	}
}

func cloneCurdOption(source CurdOption) CurdOption {
	result := source
	result.API = append([]APIType(nil), source.API...)
	result.InfoIgnoreProperty = append([]ColumnRef(nil), source.InfoIgnoreProperty...)
	result.HiddenFields = append([]ColumnRef(nil), source.HiddenFields...)
	result.ReadonlyFields = append([]ColumnRef(nil), source.ReadonlyFields...)
	result.SortFields = append([]ColumnRef(nil), source.SortFields...)
	result.PageQueryOp = cloneProvider(source.PageQueryOp)
	result.ListQueryOp = cloneProvider(source.ListQueryOp)
	if source.URLTag != nil {
		tag := *source.URLTag
		tag.URL = append([]APIType(nil), source.URLTag.URL...)
		result.URLTag = &tag
	}

	return result
}

func cloneOptions(source RouterOptions) RouterOptions {
	result := source
	result.Alias = append([]string(nil), source.Alias...)
	result.Middleware = append([]MiddlewareRef(nil), source.Middleware...)
	if source.Sensitive != nil {
		result.Sensitive = Bool(*source.Sensitive)
	}

	return result
}

func cloneRoutes(source []Route) []Route {
	result := make([]Route, len(source))
	for index, value := range source {
		result[index] = cloneRoute(value)
	}

	return result
}

func cloneRoute(source Route) Route {
	result := source
	if result.Bind == "" {
		result.Bind = BindAuto
	}
	result.Middleware = append([]MiddlewareRef(nil), source.Middleware...)
	result.Tags = make([]URLTag, len(source.Tags))
	for index, tag := range source.Tags {
		result.Tags[index] = URLTag{Name: tag.Name, URL: append([]APIType(nil), tag.URL...)}
	}

	return result
}

func requireDefinition(value Definition) (*definition, error) {
	current, matches := value.(*definition)
	if !matches || current == nil {
		return nil, exception.Core("Controller Definition 无效")
	}

	return current, nil
}

func requireCurd(value Definition) (CurdOption, error) {
	current, err := requireDefinition(value)
	if err != nil {
		return CurdOption{}, err
	}
	if current.curd == nil {
		return CurdOption{}, exception.Core("Controller 未声明 Curd")
	}

	return cloneCurdOption(*current.curd), nil
}

func isAPI(value APIType) bool {
	switch value {
	case Add, Delete, Update, Info, List, Page:
		return true
	default:
		return false
	}
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func panicCore(format string, arguments ...any) {
	panic(exception.Core(fmt.Sprintf(format, arguments...)))
}
