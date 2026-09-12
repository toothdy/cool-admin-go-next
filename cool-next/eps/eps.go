package eps

import (
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/route"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

// EPS 自动投影所需的框架运行时输入
type Input struct {
	Graph       module.Graph
	Controllers []ControllerInput
	Descriptors []gnentity.RuntimeDescriptor
}

// 静态 Graph Controller 与运行时 Definition 的映射
type ControllerInput struct {
	Key        string
	Definition gnctrl.Definition
}

// 后台与 App 的最终 EPS 视图
type Views struct {
	Admin map[string][]Controller `json:"admin"`
	App   map[string][]Controller `json:"app"`
}

// cool-admin-vue 消费的 EPS Controller
type Controller struct {
	Module      string      `json:"module"`
	Name        string      `json:"name,omitempty"`
	Prefix      string      `json:"prefix"`
	Info        Info        `json:"info"`
	API         []API       `json:"api"`
	Columns     []Column    `json:"columns"`
	PageQueryOp PageQueryOp `json:"pageQueryOp"`
	PageColumns []Column    `json:"pageColumns"`
}

// EPS Controller 元信息
type Info struct {
	Type InfoType `json:"type"`
}

// EPS Controller 类型元信息
type InfoType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// 路由契约
type API struct {
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Summary     string         `json:"summary"`
	DTS         map[string]any `json:"dts"`
	Tag         string         `json:"tag"`
	Prefix      string         `json:"prefix"`
	IgnoreToken bool           `json:"ignoreToken"`
}

// EPS 实体字段契约
type Column struct {
	PropertyName string `json:"propertyName"`
	Type         string `json:"type"`
	Length       string `json:"length"`
	Comment      string `json:"comment"`
	Nullable     bool   `json:"nullable"`
	DefaultValue any    `json:"defaultValue,omitempty"`
	Dict         any    `json:"dict,omitempty"`
	Source       string `json:"source"`
}

// EPS 分页查询配置
type PageQueryOp struct {
	KeyWordLikeFields []string     `json:"keyWordLikeFields"`
	FieldEq           []QueryField `json:"fieldEq"`
	FieldLike         []QueryField `json:"fieldLike"`
}

// 查询列与真实请求参数名
type QueryField struct {
	Column       string
	RequestParam string
}

// 编码 Node 兼容的字符串或对象联合格式
func (field QueryField) MarshalJSON() ([]byte, error) {
	name := field.Column
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		name = name[index+1:]
	}
	if field.RequestParam == name {
		return json.Marshal(field.Column)
	}

	return json.Marshal(struct {
		Column       string `json:"column"`
		RequestParam string `json:"requestParam"`
	}{field.Column, field.RequestParam})
}

type compiler struct {
	input       Input
	controllers map[string]gnctrl.DefinitionSnapshot
	descriptors descriptorResolver
	graphTables map[string]string
}

type descriptorResolver struct {
	byType  map[reflect.Type]gnentity.RuntimeDescriptor
	byTable map[string]gnentity.RuntimeDescriptor
}

// 按实体类型解析 Descriptor
func (resolver descriptorResolver) Resolve(value any) (gnentity.Metadata, bool) {
	descriptor, exists := resolver.byType[reflect.TypeOf(value)]

	return descriptor, exists
}

type routeBucket struct {
	controller Controller
	hasCRUD    bool
}

var publishedViews atomic.Pointer[Views]

// 从已校验 Graph 与运行时定义生成最终 EPS 契约
func CompileViews(input Input, includeDevelopment bool) (*Views, error) {
	current := &compiler{
		input:       input,
		controllers: make(map[string]gnctrl.DefinitionSnapshot),
		descriptors: descriptorResolver{
			byType:  make(map[reflect.Type]gnentity.RuntimeDescriptor),
			byTable: make(map[string]gnentity.RuntimeDescriptor),
		},
		graphTables: make(map[string]string),
	}
	if err := current.validate(); err != nil {
		return nil, err
	}
	views := &Views{
		Admin: make(map[string][]Controller),
		App:   make(map[string][]Controller),
	}
	for _, definition := range input.Graph.Routes().Controllers() {
		if !includeDevelopment && definition.DevelopmentOnly() {
			continue
		}
		items, area, err := current.compileController(definition, includeDevelopment)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue
		}
		target := views.Admin
		if area == gnctrl.AreaApp {
			target = views.App
		}
		target[definition.Module()] = append(target[definition.Module()], items...)
	}

	return views, nil
}

func (current *compiler) validate() error {
	if !current.input.Graph.IsValidated() {
		return exception.Core("EPS 输入必须使用已校验的模块 Graph")
	}
	for _, descriptor := range current.input.Graph.Descriptors() {
		current.graphTables[descriptor.Table()] = descriptor.Module()
	}
	for _, descriptor := range current.input.Descriptors {
		if isNil(descriptor) {
			return exception.Core("EPS Descriptor 不能为空")
		}
		if _, exists := current.graphTables[descriptor.Table()]; !exists {
			return exception.Core(fmt.Sprintf("EPS Descriptor 表 %s 未在 Graph 登记", descriptor.Table()))
		}
		if _, exists := current.descriptors.byTable[descriptor.Table()]; exists {
			return exception.Core(fmt.Sprintf("EPS Descriptor 表 %s 重复", descriptor.Table()))
		}
		if _, exists := current.descriptors.byType[descriptor.EntityType()]; exists {
			return exception.Core(fmt.Sprintf("EPS Descriptor 实体 %s 重复", descriptor.EntityType()))
		}
		current.descriptors.byTable[descriptor.Table()] = descriptor
		current.descriptors.byType[descriptor.EntityType()] = descriptor
	}
	for table := range current.graphTables {
		if _, exists := current.descriptors.byTable[table]; !exists {
			return exception.Core(fmt.Sprintf("EPS 缺少表 %s 的运行时 Descriptor", table))
		}
	}
	graphControllers := make(map[string]bool)
	for _, definition := range current.input.Graph.Routes().Controllers() {
		graphControllers[definition.Key()] = true
	}
	for _, input := range current.input.Controllers {
		if !graphControllers[input.Key] {
			return exception.Core(fmt.Sprintf("EPS Controller %s 未在 Graph 登记", input.Key))
		}
		if _, exists := current.controllers[input.Key]; exists {
			return exception.Core(fmt.Sprintf("EPS Controller %s 重复", input.Key))
		}
		snapshot, err := gnctrl.Snapshot(input.Definition)
		if err != nil {
			return exception.WrapCore(err, fmt.Sprintf("读取 EPS Controller %s 失败", input.Key))
		}
		if snapshot.Area != gnctrl.AreaAdmin && snapshot.Area != gnctrl.AreaApp {
			return exception.Core(fmt.Sprintf("EPS Controller %s 的区域无效", input.Key))
		}
		current.controllers[input.Key] = snapshot
	}
	for key := range graphControllers {
		if _, exists := current.controllers[key]; !exists {
			return exception.Core(fmt.Sprintf("EPS Controller %s 缺少运行时 Definition", key))
		}
	}

	return nil
}

func (current *compiler) compileController(
	definition route.Controller,
	includeDevelopment bool,
) ([]Controller, gnctrl.Area, error) {
	snapshot := current.controllers[definition.Key()]
	buckets := make([]routeBucket, 0)
	indexes := make(map[string]int)
	customRoutes := snapshot.Routes
	for _, currentRoute := range current.controllerRoutes(definition.Key()) {
		if !includeDevelopment && currentRoute.DevelopmentOnly() {
			continue
		}
		if strings.ContainsAny(currentRoute.Path(), "{}:") {
			continue
		}
		prefix, err := routePrefix(currentRoute, customRoutes)
		if err != nil {
			return nil, snapshot.Area, exception.WrapCore(err, fmt.Sprintf("EPS Controller %s 路由 %s %s 无效", definition.Key(), currentRoute.Method(), currentRoute.Path()))
		}
		index, exists := indexes[prefix]
		if !exists {
			index = len(buckets)
			indexes[prefix] = index
			buckets = append(buckets, routeBucket{controller: emptyController(definition, prefix)})
		}
		bucket := &buckets[index]
		bucket.controller.API = append(bucket.controller.API, compileAPI(currentRoute, prefix))
		if currentRoute.Kind() == route.KindCRUD {
			bucket.hasCRUD = true
		}
	}

	result := make([]Controller, 0, len(buckets))
	for index := range buckets {
		bucket := &buckets[index]
		if bucket.hasCRUD {
			if snapshot.Curd == nil {
				return nil, snapshot.Area, exception.Core(fmt.Sprintf("CRUD Controller %s 缺少运行时 CurdOption", definition.Key()))
			}
			if err := current.compileCRUD(&bucket.controller, *snapshot.Curd); err != nil {
				return nil, snapshot.Area, exception.WrapCore(err, fmt.Sprintf("编译 EPS Controller %s 失败", definition.Key()))
			}
		}
		result = append(result, bucket.controller)
	}

	return result, snapshot.Area, nil
}

func (current *compiler) controllerRoutes(key string) []route.Route {
	result := make([]route.Route, 0)
	for _, route := range current.input.Graph.Routes().Routes() {
		if route.Controller() == key {
			result = append(result, route)
		}
	}

	return result
}

func routePrefix(currentRoute route.Route, custom []gnctrl.Route) (string, error) {
	if currentRoute.Kind() == route.KindCRUD {
		prefix := path.Dir(currentRoute.Path())
		if prefix == "." || prefix == "/" {
			return strings.TrimSuffix(prefix, "/"), nil
		}

		return prefix, nil
	}
	matched := ""
	for _, candidate := range custom {
		if !strings.EqualFold(candidate.Method, currentRoute.Method()) || !strings.HasSuffix(currentRoute.Path(), candidate.Path) {
			continue
		}
		if len(candidate.Path) > len(matched) {
			matched = candidate.Path
		}
	}
	if matched != "" {
		return strings.TrimSuffix(currentRoute.Path(), matched), nil
	}

	return "", exception.Core("找不到对应的运行时自定义 Route")
}

func emptyController(definition route.Controller, prefix string) Controller {
	return Controller{
		Module: definition.Module(),
		Prefix: prefix,
		Info: Info{Type: InfoType{
			Name:        controllerName(prefix),
			Description: definition.Description(),
		}},
		API:         make([]API, 0),
		Columns:     make([]Column, 0),
		PageQueryOp: emptyPageQueryOp(),
		PageColumns: make([]Column, 0),
	}
}

func compileAPI(route route.Route, prefix string) API {
	relative := strings.TrimPrefix(route.Path(), prefix)
	if relative == "" {
		relative = "/"
	}

	return API{
		Method:      strings.ToLower(route.Method()),
		Path:        relative,
		Summary:     route.Summary(),
		DTS:         make(map[string]any),
		Prefix:      prefix,
		IgnoreToken: slices.Contains(route.Tags(), gnctrl.TagIgnoreToken),
	}
}

func (current *compiler) compileCRUD(target *Controller, option gnctrl.CurdOption) error {
	descriptor, exists := current.descriptors.byType[reflect.TypeOf(option.Entity)]
	if !exists {
		return exception.Core(fmt.Sprintf("实体 %T 的 Descriptor 不存在", option.Entity))
	}
	hiddenColumns, err := crud.ProjectColumns(current.descriptors, option.Entity, option.HiddenFields)
	if err != nil {
		return exception.WrapCore(err, "解析隐藏字段失败")
	}
	hidden := make(map[string]bool, len(hiddenColumns))
	for _, column := range hiddenColumns {
		hidden[column.Field.Name()] = true
	}
	target.Name = entityName(descriptor.Table())
	target.Columns = compileColumns(descriptor.Fields(), hidden)

	projection, static, err := gnctrl.ProjectQuery(option.PageQueryOp, current.descriptors, option.Entity)
	if err != nil {
		return exception.WrapCore(err, "投影分页查询失败")
	}
	if !static {
		return nil
	}
	target.PageQueryOp = compilePageQuery(projection, descriptor, hidden)
	target.PageColumns = pageColumns(projection.Select, descriptor, hidden)

	return nil
}

func compileColumns(fields []gnentity.Field, hidden map[string]bool) []Column {
	columns := make([]Column, 0, len(fields))
	trailing := make([]Column, 0, 2)
	for _, field := range fields {
		if hidden[field.Name()] {
			continue
		}
		column := compileColumn(field, field.JSONName(), "a."+field.JSONName())
		if isTimeField(field) {
			trailing = append(trailing, column)
			continue
		}
		columns = append(columns, column)
	}

	return append(columns, trailing...)
}

func compileColumn(field gnentity.Field, propertyName, source string) Column {
	constraints := field.Constraints()
	result := Column{
		PropertyName: propertyName,
		Type:         columnType(field),
		Comment:      field.Description(),
		Nullable:     field.Nullable(),
		Source:       source,
	}
	if constraints.HasSize {
		result.Length = strconv.FormatUint(constraints.Size, 10)
	}
	if constraints.HasDefault {
		result.DefaultValue = parseDefault(field.LogicalType(), constraints.Default)
	}

	return result
}

func compilePageQuery(projection crud.QueryProjection, root gnentity.RuntimeDescriptor, hidden map[string]bool) PageQueryOp {
	result := emptyPageQueryOp()
	for _, column := range projection.KeyWordLikeFields {
		if visibleColumn(column, root, hidden) {
			result.KeyWordLikeFields = append(result.KeyWordLikeFields, column.Source)
		}
	}
	for _, match := range projection.FieldEq {
		if visibleColumn(match.Column, root, hidden) {
			result.FieldEq = append(result.FieldEq, QueryField{Column: match.Column.Source, RequestParam: match.RequestParam})
		}
	}
	for _, match := range projection.FieldLike {
		if visibleColumn(match.Column, root, hidden) {
			result.FieldLike = append(result.FieldLike, QueryField{Column: match.Column.Source, RequestParam: match.RequestParam})
		}
	}

	return result
}

func pageColumns(selects []crud.QuerySelect, root gnentity.RuntimeDescriptor, hidden map[string]bool) []Column {
	columns := make([]Column, 0, len(selects))
	trailing := make([]Column, 0, 2)
	for _, selected := range selects {
		if !visibleColumn(selected.Column, root, hidden) {
			continue
		}
		column := compileColumn(selected.Column.Field, selected.Name, selected.Column.Source)
		if isTimeProperty(column.PropertyName) {
			trailing = append(trailing, column)
			continue
		}
		columns = append(columns, column)
	}

	return append(columns, trailing...)
}

func visibleColumn(column crud.QueryColumn, root gnentity.RuntimeDescriptor, hidden map[string]bool) bool {
	if column.Field == nil || !column.Field.Persistent() {
		return false
	}
	if column.Descriptor.Table() != root.Table() || !strings.HasPrefix(column.Source, "a.") {
		return true
	}

	return !hidden[column.Field.Name()]
}

func emptyPageQueryOp() PageQueryOp {
	return PageQueryOp{
		KeyWordLikeFields: make([]string, 0),
		FieldEq:           make([]QueryField, 0),
		FieldLike:         make([]QueryField, 0),
	}
}

func columnType(field gnentity.Field) string {
	switch field.LogicalType() {
	case gnentity.LogicalBool:
		return "boolean"
	case gnentity.LogicalInt, gnentity.LogicalUint:
		return "number"
	case gnentity.LogicalFloat:
		if !field.Constraints().HasPrecision {
			return "number"
		}
		return "decimal"
	case gnentity.LogicalString:
		if !field.Constraints().HasSize {
			return "text"
		}
		return "string"
	case gnentity.LogicalBytes:
		return "text"
	case gnentity.LogicalTime:
		if field.SystemMaintained() {
			return "varchar"
		}
		return "date"
	case gnentity.LogicalJSON:
		return "json"
	default:
		return string(field.LogicalType())
	}
}

func entityName(table string) string {
	var result strings.Builder
	for _, part := range strings.Split(table, "_") {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		result.WriteString(part[1:])
	}
	result.WriteString("Entity")

	return result.String()
}

func parseDefault(logicalType gnentity.LogicalType, value string) any {
	switch logicalType {
	case gnentity.LogicalBool:
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	case gnentity.LogicalInt:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	case gnentity.LogicalUint:
		if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
			return parsed
		}
	case gnentity.LogicalFloat:
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}

	return value
}

func controllerName(prefix string) string {
	parts := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(parts) == 0 {
		return ""
	}

	return parts[len(parts)-1]
}

func isTimeField(field gnentity.Field) bool {
	return isTimeProperty(field.JSONName())
}

func isTimeProperty(name string) bool {
	return name == "createTime" || name == "updateTime"
}

func isNil(value any) bool {
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

// 发布启动期已编译的 EPS 快照
func PublishViews(views *Views) error {
	if views == nil {
		return exception.Core("EPS 视图不能为空")
	}
	value := *views
	publishedViews.Store(&value)

	return nil
}

// 返回已发布的后台 EPS 视图
func AdminView() (map[string][]Controller, error) {
	views := publishedViews.Load()
	if views == nil {
		return nil, exception.Core("EPS 视图尚未发布")
	}

	return views.Admin, nil
}

// 返回已发布的 App EPS 视图
func AppView() (map[string][]Controller, error) {
	views := publishedViews.Load()
	if views == nil {
		return nil, exception.Core("EPS 视图尚未发布")
	}

	return views.App, nil
}
