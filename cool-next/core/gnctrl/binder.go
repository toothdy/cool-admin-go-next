package gnctrl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/gconv"
	"github.com/gogf/gf/v2/util/gvalid"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

const viewFieldPrefix = "_" // 前端派生视图字段前缀

var (
	uploadFileType     = reflect.TypeFor[*ghttp.UploadFile]()
	uploadFilesType    = reflect.TypeFor[ghttp.UploadFiles]()
	multipartFileType  = reflect.TypeFor[*multipart.FileHeader]()
	multipartFilesType = reflect.TypeFor[[]*multipart.FileHeader]()
)

// 严格 HTTP 请求绑定器
type Binder struct {
	config crud.Config
}

// DTO 字段绑定信息
type dtoField struct {
	index []int
	name  string
	typ   reflect.Type
}

// 创建严格 HTTP 请求绑定器
func NewBinder(config crud.Config) (*Binder, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &Binder{config: config}, nil
}

// 返回保留取消、截止时间和 Trace Context 的请求上下文
func RequestContext(request *ghttp.Request) (context.Context, error) {
	if request == nil || request.Request == nil {
		return nil, exception.Validate("HTTP 请求不能为空")
	}
	ctx := request.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return ctx, nil
}

// 按声明来源绑定并校验自定义 DTO
func (binder *Binder) BindDTO(request *ghttp.Request, source BindSource, target any) error {
	ctx, err := binder.require(request)
	if err != nil {
		return err
	}
	targetType, err := requireDTOTarget(target)
	if err != nil {
		return err
	}

	switch source {
	case BindJSON:
		err = binder.bindJSON(request, target)
	case BindQuery:
		err = bindDTOMap(target, targetType, source, valuesMap(request.URL.Query()))
	case BindForm:
		err = binder.bindForm(request, target, targetType)
	case BindPath:
		data := make(map[string]any, len(request.GetRouterMap()))
		for name, value := range request.GetRouterMap() {
			data[name] = value
		}
		err = bindDTOMap(target, targetType, source, data)
	case BindFile:
		err = binder.bindFiles(request, target, targetType)
	default:
		return exception.Validate(fmt.Sprintf("请求绑定来源 %q 无效", source))
	}
	if err != nil {
		return err
	}
	if validationErr := gvalid.New().Data(target).Run(ctx); validationErr != nil {
		return exception.WrapValidate(validationErr, "DTO 校验失败")
	}

	return nil
}

// 绑定单对象或顶层数组新增输入
func BindAdd[E any, ID comparable](
	binder *Binder,
	request *ghttp.Request,
	descriptor gnentity.Descriptor[E, ID],
) (gnservice.AddInput[E], error) {
	var zero gnservice.AddInput[E]
	if _, err := binder.require(request); err != nil {
		return zero, err
	}
	body, err := binder.readBody(request)
	if err != nil {
		return zero, err
	}
	items, isMany, err := decodeEntityItems[E, ID](body, descriptor, false, binder.config.BatchLimit)
	if err != nil {
		return zero, err
	}
	if isMany {
		return gnservice.NewAddArray[E, ID](descriptor, items)
	}

	return gnservice.NewAddObject[E, ID](descriptor, items[0])
}

// 绑定并规范化删除 ID
func BindDelete[E any, ID comparable](
	binder *Binder,
	request *ghttp.Request,
	descriptor gnentity.Descriptor[E, ID],
) (gnservice.DeleteInput[ID], error) {
	var zero gnservice.DeleteInput[ID]
	if _, err := binder.require(request); err != nil {
		return zero, err
	}
	body, err := binder.readBody(request)
	if err != nil {
		return zero, err
	}
	raw, err := deleteRawValue(body)
	if err != nil {
		return zero, err
	}
	ids, err := decodeIDs[ID](raw, binder.config.BatchLimit)
	if err != nil {
		return zero, err
	}

	return gnservice.NewDeleteInput[E](descriptor, ids)
}

// 绑定单对象或顶层数组更新输入
func BindUpdate[E any, ID comparable](
	binder *Binder,
	request *ghttp.Request,
	descriptor gnentity.Descriptor[E, ID],
) (gnservice.UpdateInput[E, ID], error) {
	var zero gnservice.UpdateInput[E, ID]
	if _, err := binder.require(request); err != nil {
		return zero, err
	}
	body, err := binder.readBody(request)
	if err != nil {
		return zero, err
	}
	rawItems, isMany, err := decodeRawObjects(body, binder.config.BatchLimit)
	if err != nil {
		return zero, err
	}
	items := make([]gnservice.UpdateItem[E, ID], len(rawItems))
	primary := descriptor.Primary().JSONName()
	for index, raw := range rawItems {
		idRaw, exists := raw[primary]
		if !exists {
			return zero, exception.Validate(fmt.Sprintf("更新项缺少主键 %s", primary))
		}
		id, idErr := decodeID[ID](idRaw)
		if idErr != nil {
			return zero, idErr
		}
		delete(raw, primary)
		mutable, mutableErr := decodeMutable[E, ID](raw, descriptor)
		if mutableErr != nil {
			return zero, mutableErr
		}
		items[index], err = gnservice.NewUpdateItem(descriptor, id, mutable)
		if err != nil {
			return zero, err
		}
	}
	if isMany {
		return gnservice.NewUpdateArray(descriptor, items)
	}

	return gnservice.NewUpdateObject(descriptor, items[0])
}

// 绑定详情主键
func BindInfo[ID comparable](binder *Binder, request *ghttp.Request) (ID, error) {
	var zero ID
	if _, err := binder.require(request); err != nil {
		return zero, err
	}
	query := request.URL.Query()
	if len(query) != 1 {
		return zero, exception.Validate("Info 只允许 id 查询参数")
	}
	values, exists := query["id"]
	if !exists || len(values) != 1 {
		return zero, exception.Validate("Info 主键 id 无效")
	}

	return parseIDText[ID](values[0])
}

// 绑定 List 或 Page 公共查询参数
func BindCRUDQuery(binder *Binder, request *ghttp.Request, action crud.Action) (gnservice.Query, error) {
	if _, err := binder.require(request); err != nil {
		return gnservice.Query{}, err
	}
	if action != crud.ActionList && action != crud.ActionPage {
		return gnservice.Query{}, exception.Validate("查询动作必须是 list 或 page")
	}
	body, err := binder.readBody(request)
	if err != nil {
		return gnservice.Query{}, err
	}
	data, err := decodeQueryObject(body)
	if err != nil {
		return gnservice.Query{}, err
	}
	if err = checkOrder(data); err != nil {
		return gnservice.Query{}, err
	}
	page, size, err := binder.queryWindow(data, action)
	if err != nil {
		return gnservice.Query{}, err
	}
	values := make([]crud.RequestValue, 0, len(data))
	for name, value := range data {
		if value == nil {
			values = append(values, crud.RequestNull(name))
			continue
		}
		values = append(values, crud.RequestField(name, value))
	}
	queryRequest, err := crud.NewQueryRequest(values)
	if err != nil {
		return gnservice.Query{}, err
	}

	if action == crud.ActionList {
		return gnservice.NewListQuery(queryRequest, size)
	}

	return gnservice.NewQuery(queryRequest, page, size)
}

func (binder *Binder) require(request *ghttp.Request) (context.Context, error) {
	if binder == nil {
		return nil, exception.Core("HTTP Binder 未初始化")
	}
	if err := binder.config.Validate(); err != nil {
		return nil, err
	}

	return RequestContext(request)
}

func (binder *Binder) readBody(request *ghttp.Request) ([]byte, error) {
	if request.ContentLength > binder.config.BodyLimit {
		return nil, exception.Validate(fmt.Sprintf("请求体超过 %d 字节上限", binder.config.BodyLimit))
	}
	content, err := io.ReadAll(io.LimitReader(request.Body, binder.config.BodyLimit+1))
	request.Body = io.NopCloser(bytes.NewReader(content))
	if err != nil {
		return nil, exception.WrapValidate(err, "读取请求体失败")
	}
	if int64(len(content)) > binder.config.BodyLimit {
		return nil, exception.Validate(fmt.Sprintf("请求体超过 %d 字节上限", binder.config.BodyLimit))
	}

	return content, nil
}

func (binder *Binder) bindForm(request *ghttp.Request, target any, targetType reflect.Type) error {
	if _, err := binder.readBody(request); err != nil {
		return err
	}
	if err := request.Request.ParseForm(); err != nil {
		return exception.WrapValidate(err, "Form 请求体无效")
	}

	return bindDTOMap(target, targetType, BindForm, valuesMap(request.PostForm))
}

func (binder *Binder) bindJSON(request *ghttp.Request, target any) error {
	body, err := binder.readBody(request)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return exception.Validate("JSON 请求体不能为空")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return exception.WrapValidate(err, "JSON 请求体无效")
	}

	return requireJSONEnd(decoder)
}

func (binder *Binder) bindFiles(request *ghttp.Request, target any, targetType reflect.Type) error {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		return binder.bindJSON(request, target)
	}
	if request.ContentLength > binder.config.MultipartBodyLimit {
		return exception.Validate(fmt.Sprintf("文件上传请求超过 %d 字节上限", binder.config.MultipartBodyLimit))
	}
	request.Body = http.MaxBytesReader(nil, request.Body, binder.config.MultipartBodyLimit)
	if err := request.Request.ParseMultipartForm(binder.config.BodyLimit); err != nil {
		return exception.WrapValidate(err, "文件上传请求无效")
	}
	data := map[string]any{}
	files := map[string][]*multipart.FileHeader{}
	if request.MultipartForm != nil {
		data = valuesMap(request.MultipartForm.Value)
		files = request.MultipartForm.File
	}
	fields, err := dtoFields(targetType, BindFile)
	if err != nil {
		return err
	}
	for name := range files {
		field, exists := fields[name]
		if !exists || !isFileType(field.typ) {
			return exception.Validate(fmt.Sprintf("DTO 存在未知文件字段 %s", name))
		}
	}
	if err = bindFields(target, targetType, BindFile, data, fields); err != nil {
		return err
	}
	value := reflect.ValueOf(target).Elem()
	for name, field := range fields {
		if !isFileType(field.typ) {
			continue
		}
		if err = setFileField(value, field, files[name]); err != nil {
			return err
		}
	}

	return nil
}

func bindDTOMap(target any, targetType reflect.Type, source BindSource, data map[string]any) error {
	fields, err := dtoFields(targetType, source)
	if err != nil {
		return err
	}

	return bindFields(target, targetType, source, data, fields)
}

func bindFields(
	target any,
	targetType reflect.Type,
	source BindSource,
	data map[string]any,
	fields map[string]dtoField,
) error {
	for name := range data {
		if _, exists := fields[name]; !exists {
			return exception.Validate(fmt.Sprintf("DTO 存在未知字段 %s", name))
		}
	}
	mapping := make(map[string]string, len(fields))
	for name, field := range fields {
		if !isFileType(field.typ) {
			mapping[name] = fieldPath(targetType, field.index)
		}
	}
	if err := gconv.Struct(data, target, mapping); err != nil {
		return exception.WrapValidate(err, fmt.Sprintf("%s DTO 绑定失败", source))
	}

	return nil
}

func requireDTOTarget(target any) (reflect.Type, error) {
	value := reflect.ValueOf(target)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return nil, exception.Core("DTO 目标必须是非 nil struct 指针")
	}

	return value.Elem().Type(), nil
}

func dtoFields(target reflect.Type, source BindSource) (map[string]dtoField, error) {
	result := make(map[string]dtoField)
	if err := collectDTOFields(target, source, nil, result); err != nil {
		return nil, err
	}

	return result, nil
}

func collectDTOFields(target reflect.Type, source BindSource, prefix []int, result map[string]dtoField) error {
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		if !field.IsExported() {
			continue
		}
		path := append(append([]int(nil), prefix...), index)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if err := collectDTOFields(field.Type, source, path, result); err != nil {
				return err
			}
			continue
		}
		name, exists := dtoFieldName(field, source)
		if !exists {
			continue
		}
		if _, duplicated := result[name]; duplicated {
			return exception.Core(fmt.Sprintf("DTO 字段名 %s 重复", name))
		}
		result[name] = dtoField{index: path, name: name, typ: field.Type}
	}

	return nil
}

func dtoFieldName(field reflect.StructField, source BindSource) (string, bool) {
	if in := field.Tag.Get("in"); in != "" && BindSource(in) != source && source != BindFile {
		return "", false
	}
	tags := []string{string(source), "json"}
	if source == BindFile {
		tags = []string{"file", "form", "json"}
	}
	for _, tag := range tags {
		value := strings.Split(field.Tag.Get(tag), ",")[0]
		if value == "-" {
			return "", false
		}
		if value != "" {
			return value, true
		}
	}

	return field.Name, true
}

func fieldPath(target reflect.Type, indexes []int) string {
	parts := make([]string, len(indexes))
	current := target
	for index, fieldIndex := range indexes {
		field := current.Field(fieldIndex)
		parts[index] = field.Name
		current = field.Type
	}

	return strings.Join(parts, ".")
}

func isFileType(value reflect.Type) bool {
	return value == uploadFileType || value == uploadFilesType || value == multipartFileType || value == multipartFilesType
}

func setFileField(target reflect.Value, field dtoField, files []*multipart.FileHeader) error {
	value := target.FieldByIndex(field.index)
	switch field.typ {
	case uploadFileType:
		if len(files) > 1 {
			return exception.Validate(fmt.Sprintf("文件字段 %s 只允许一个文件", field.name))
		}
		if len(files) == 1 {
			value.Set(reflect.ValueOf(&ghttp.UploadFile{FileHeader: files[0]}))
		}
	case uploadFilesType:
		result := make(ghttp.UploadFiles, len(files))
		for index, file := range files {
			result[index] = &ghttp.UploadFile{FileHeader: file}
		}
		value.Set(reflect.ValueOf(result))
	case multipartFileType:
		if len(files) > 1 {
			return exception.Validate(fmt.Sprintf("文件字段 %s 只允许一个文件", field.name))
		}
		if len(files) == 1 {
			value.Set(reflect.ValueOf(files[0]))
		}
	case multipartFilesType:
		value.Set(reflect.ValueOf(append([]*multipart.FileHeader(nil), files...)))
	}

	return nil
}

func decodeEntityItems[E any, ID comparable](
	body []byte,
	descriptor gnentity.Descriptor[E, ID],
	removePrimary bool,
	limit int,
) ([]*gnservice.Mutable[E], bool, error) {
	rawItems, isMany, err := decodeRawObjects(body, limit)
	if err != nil {
		return nil, false, err
	}
	items := make([]*gnservice.Mutable[E], len(rawItems))
	for index, raw := range rawItems {
		if removePrimary {
			delete(raw, descriptor.Primary().JSONName())
		}
		items[index], err = decodeMutable[E, ID](raw, descriptor)
		if err != nil {
			return nil, false, err
		}
	}

	return items, isMany, nil
}

func decodeRawObjects(body []byte, limit int) ([]map[string]json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, false, exception.Validate("JSON 请求体不能为空")
	}
	switch trimmed[0] {
	case '{':
		var item map[string]json.RawMessage
		if err := decodeJSON(trimmed, &item); err != nil {
			return nil, false, err
		}
		return []map[string]json.RawMessage{item}, false, nil
	case '[':
		var items []map[string]json.RawMessage
		if err := decodeJSON(trimmed, &items); err != nil {
			return nil, false, err
		}
		if len(items) == 0 {
			return nil, true, exception.Validate("JSON 顶层数组不能为空")
		}
		if len(items) > limit {
			return nil, true, exception.Validate(fmt.Sprintf("批量请求数量超过 %d 上限", limit))
		}
		return items, true, nil
	default:
		return nil, false, exception.Validate("JSON 请求体必须是对象或顶层数组")
	}
}

func decodeMutable[E any, ID comparable](
	raw map[string]json.RawMessage,
	descriptor gnentity.Descriptor[E, ID],
) (*gnservice.Mutable[E], error) {
	fields := make([]gnservice.FieldValue, 0, len(raw))
	for name, encoded := range raw {
		field, exists := descriptor.JSON(name)
		if !exists {
			// cool-admin-vue 会把派生视图字段挂到实体行并整行回传
			// 按约定丢弃下划线前缀字段，其余未知字段仍报错以保留拼写校验
			if strings.HasPrefix(name, viewFieldPrefix) {
				continue
			}

			return nil, exception.Validate(fmt.Sprintf("实体不存在 JSON 字段 %s", name))
		}
		if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
			fields = append(fields, gnservice.Null(name))
			continue
		}
		target := field.GoType()
		if target.Kind() == reflect.Pointer {
			target = target.Elem()
		}
		value := reflect.New(target)
		var err error
		if target.Kind() == reflect.Bool {
			switch string(bytes.TrimSpace(encoded)) {
			case "0":
				value.Elem().SetBool(false)
			case "1":
				value.Elem().SetBool(true)
			default:
				err = decodeJSON(encoded, value.Interface())
			}
		} else {
			err = decodeJSON(encoded, value.Interface())
		}
		if err != nil {
			return nil, exception.WrapValidate(err, fmt.Sprintf("实体字段 %s 无效", name))
		}
		fields = append(fields, gnservice.Value(name, value.Elem().Interface()))
	}

	return gnservice.NewMutable[E, ID](descriptor, fields)
}

func deleteRawValue(body []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, exception.Validate("Delete 请求体不能为空")
	}
	if trimmed[0] != '{' {
		return append(json.RawMessage(nil), trimmed...), nil
	}
	var object map[string]json.RawMessage
	if err := decodeJSON(trimmed, &object); err != nil {
		return nil, err
	}
	if len(object) != 1 {
		return nil, exception.Validate("Delete 请求体只允许 ids 字段")
	}
	raw, exists := object["ids"]
	if !exists {
		return nil, exception.Validate("Delete 请求体缺少 ids 字段")
	}

	return raw, nil
}

func decodeIDs[ID comparable](raw json.RawMessage, limit int) ([]ID, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, exception.Validate("Delete ID 不能为空")
	}
	if trimmed[0] == '[' {
		var values []json.RawMessage
		if err := decodeJSON(trimmed, &values); err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, exception.Validate("Delete ID 数组不能为空")
		}
		if len(values) > limit {
			return nil, exception.Validate(fmt.Sprintf("批量请求数量超过 %d 上限", limit))
		}
		ids := make([]ID, len(values))
		for index, value := range values {
			id, err := decodeID[ID](value)
			if err != nil {
				return nil, err
			}
			ids[index] = id
		}
		return ids, nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := decodeJSON(trimmed, &value); err != nil {
			return nil, err
		}
		parts := strings.Split(value, ",")
		if len(parts) > limit {
			return nil, exception.Validate(fmt.Sprintf("批量请求数量超过 %d 上限", limit))
		}
		ids := make([]ID, len(parts))
		for index, part := range parts {
			id, err := parseIDText[ID](strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			ids[index] = id
		}
		return ids, nil
	}
	id, err := decodeID[ID](trimmed)
	if err != nil {
		return nil, err
	}

	return []ID{id}, nil
}

func decodeID[ID comparable](raw json.RawMessage) (ID, error) {
	var zero ID
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string
		if err := decodeJSON(trimmed, &value); err != nil {
			return zero, err
		}
		return parseIDText[ID](value)
	}
	var result ID
	if err := decodeJSON(trimmed, &result); err != nil {
		return zero, exception.WrapValidate(err, "主键类型无效")
	}
	if reflect.ValueOf(result).IsZero() {
		return zero, exception.Validate("主键必须大于 0")
	}

	return result, nil
}

func parseIDText[ID comparable](value string) (ID, error) {
	var zero ID
	value = strings.TrimSpace(value)
	if value == "" {
		return zero, exception.Validate("主键不能为空")
	}
	target := reflect.TypeFor[ID]()
	result := reflect.New(target).Elem()
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, target.Bits())
		if err != nil || parsed <= 0 {
			return zero, exception.Validate(fmt.Sprintf("主键 %q 无效", value))
		}
		result.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, target.Bits())
		if err != nil || parsed == 0 {
			return zero, exception.Validate(fmt.Sprintf("主键 %q 无效", value))
		}
		result.SetUint(parsed)
	case reflect.String:
		result.SetString(value)
	default:
		return zero, exception.Core(fmt.Sprintf("主键类型 %s 不支持 HTTP 绑定", target))
	}

	return result.Interface().(ID), nil
}

func decodeQueryObject(body []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	if trimmed[0] != '{' {
		return nil, exception.Validate("List/Page 请求体必须是 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	data := map[string]any{}
	if err := decoder.Decode(&data); err != nil {
		return nil, exception.WrapValidate(err, "List/Page 请求体无效")
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	for name, value := range data {
		data[name] = jsonValue(value)
	}

	return data, nil
}

func jsonValue(value any) any {
	switch current := value.(type) {
	case json.Number:
		if strings.ContainsAny(current.String(), ".eE") {
			parsed, _ := current.Float64()
			return parsed
		}
		parsed, err := current.Int64()
		if err == nil {
			if strconv.IntSize == 64 || parsed >= math.MinInt32 && parsed <= math.MaxInt32 {
				return int(parsed)
			}
			return parsed
		}
		unsigned, _ := strconv.ParseUint(current.String(), 10, 64)
		return unsigned
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			result[index] = jsonValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(current))
		for name, item := range current {
			result[name] = jsonValue(item)
		}
		return result
	default:
		return value
	}
}

func checkOrder(data map[string]any) error {
	orders, hasOrder, err := stringList(data["order"])
	if err != nil {
		return exception.Validate("order 必须是字符串、逗号字符串或字符串数组")
	}
	sorts, hasSort, err := stringList(data["sort"])
	if err != nil {
		return exception.Validate("sort 必须是字符串、逗号字符串或字符串数组")
	}
	if hasOrder != hasSort || hasOrder && len(orders) != len(sorts) {
		return exception.Validate("order 与 sort 数量必须一致")
	}
	for _, direction := range sorts {
		if direction != string(crud.Ascending) && direction != string(crud.Descending) {
			return exception.Validate(fmt.Sprintf("排序方向 %s 无效", direction))
		}
	}
	if hasOrder {
		data["order"] = orders
		data["sort"] = sorts
	}

	return nil
}

func stringList(value any) ([]string, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	var values []string
	switch current := value.(type) {
	case string:
		values = strings.Split(current, ",")
	case []string:
		values = append([]string(nil), current...)
	case []any:
		values = make([]string, len(current))
		for index, item := range current {
			text, matches := item.(string)
			if !matches {
				return nil, true, exception.Validate("字符串数组元素无效")
			}
			values[index] = text
		}
	default:
		return nil, true, exception.Validate("字符串列表类型无效")
	}
	for index, item := range values {
		values[index] = strings.TrimSpace(item)
		if values[index] == "" {
			return nil, true, exception.Validate("字符串列表不能为空")
		}
	}

	return values, true, nil
}

func (binder *Binder) queryWindow(data map[string]any, action crud.Action) (int, int, error) {
	page := 1
	if value, exists := data["page"]; exists {
		parsed, err := positiveInt(value, "page")
		if err != nil {
			return 0, 0, err
		}
		page = parsed
	}
	isExport := false
	if value, exists := data["isExport"]; exists {
		parsed, matches := value.(bool)
		if !matches {
			return 0, 0, exception.Validate("isExport 必须是布尔值")
		}
		isExport = parsed
	}
	if isExport {
		limit := binder.config.ExportLimit
		if value, exists := data["maxExportLimit"]; exists {
			parsed, err := positiveInt(value, "maxExportLimit")
			if err != nil {
				return 0, 0, err
			}
			if parsed > limit {
				return 0, 0, exception.Validate(fmt.Sprintf("maxExportLimit 超过 %d 上限", limit))
			}
			limit = parsed
		}
		return 1, limit, nil
	}
	if action == crud.ActionList {
		return 1, binder.config.ListLimit, nil
	}
	size := binder.config.PageSize
	if value, exists := data["size"]; exists {
		parsed, err := positiveInt(value, "size")
		if err != nil {
			return 0, 0, err
		}
		size = parsed
	}
	if size > binder.config.PageLimit {
		return 0, 0, exception.Validate(fmt.Sprintf("size 超过 %d 上限", binder.config.PageLimit))
	}

	return page, size, nil
}

func positiveInt(value any, name string) (int, error) {
	var result int64
	switch current := value.(type) {
	case int:
		result = int64(current)
	case int64:
		result = current
	case uint64:
		if current > math.MaxInt {
			return 0, exception.Validate(fmt.Sprintf("%s 超出整数范围", name))
		}
		result = int64(current)
	case float64:
		if current != math.Trunc(current) || current > math.MaxInt64 {
			return 0, exception.Validate(fmt.Sprintf("%s 必须是正整数", name))
		}
		result = int64(current)
	default:
		return 0, exception.Validate(fmt.Sprintf("%s 必须是正整数", name))
	}
	if result <= 0 || result > math.MaxInt {
		return 0, exception.Validate(fmt.Sprintf("%s 必须是正整数", name))
	}

	return int(result), nil
}

func valuesMap(values url.Values) map[string]any {
	result := make(map[string]any, len(values))
	for name, items := range values {
		if len(items) == 1 {
			result[name] = items[0]
			continue
		}
		result[name] = append([]string(nil), items...)
	}

	return result
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return exception.WrapValidate(err, "JSON 请求数据无效")
	}

	return requireJSONEnd(decoder)
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return exception.Validate("JSON 请求数据包含多个值")
		}
		return exception.WrapValidate(err, "JSON 请求数据无效")
	}

	return nil
}
