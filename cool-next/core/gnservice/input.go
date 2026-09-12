package gnservice

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

// 可写实体字段集合
type Mutable[E any] struct {
	descriptor gnentity.Metadata
	entityType reflect.Type
	idType     reflect.Type
	values     map[string]mutableValue
}

// 字段来源
type fieldSource uint8

const (
	fieldSourceClient fieldSource = iota + 1
	fieldSourceServer
)

// 单个字段的提交状态
type mutableValue struct {
	source fieldSource
	isNull bool
	data   any
}

// 外部字段值
type FieldValue struct {
	isNull      bool
	isSubmitted bool
	name        string
	data        any
}

// 新增输入
type AddInput[E any] struct {
	isMany bool
	one    *Mutable[E]
	many   []*Mutable[E]
}

// 删除输入
type DeleteInput[ID comparable] struct {
	ids []ID
}

// 单项更新输入
type UpdateItem[E any, ID comparable] struct {
	id      ID
	mutable *Mutable[E]
}

// 更新输入
type UpdateInput[E any, ID comparable] struct {
	isMany bool
	one    UpdateItem[E, ID]
	many   []UpdateItem[E, ID]
}

// 新增结果
type AddResult[ID comparable] struct {
	isMany bool
	one    ID
	many   []ID
}

// 查询请求别名
type QueryRequest = crud.QueryRequest

// 只读查询参数
type Query struct {
	request   *QueryRequest
	page      int
	size      int
	listLimit int
}

// 只读记录
type Record struct {
	values map[string]any
}

// 分页信息
type Pagination struct {
	Page  int   `json:"page"`
	Size  int   `json:"size"`
	Total int64 `json:"total"`
}

// 分页结果
type PageResult struct {
	List       []Record   `json:"list"`
	Pagination Pagination `json:"pagination"`
}

// 构造普通字段值
func Value(field string, data any) FieldValue {
	return FieldValue{
		isSubmitted: true,
		name:        field,
		data:        cloneData(data),
	}
}

// 构造显式 null 字段值
func Null(field string) FieldValue {
	return FieldValue{
		isNull:      true,
		isSubmitted: true,
		name:        field,
	}
}

// 构造可写字段集合
func NewMutable[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	fields []FieldValue,
) (*Mutable[E], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return nil, err
	}

	value := &Mutable[E]{
		descriptor: descriptor,
		entityType: descriptor.EntityType(),
		idType:     descriptor.IDType(),
		values:     make(map[string]mutableValue, len(fields)),
	}
	for _, item := range fields {
		if !item.isSubmitted {
			return nil, exception.Validate("字段值无效")
		}
		field, exists := descriptor.JSON(item.name)
		if !exists {
			return nil, exception.Validate(fmt.Sprintf("实体 %s 不存在 JSON 字段 %s", descriptor.EntityType(), item.name))
		}
		if _, exists := value.values[field.Name()]; exists {
			return nil, exception.Validate(fmt.Sprintf("实体字段 %s 重复", field.Name()))
		}
		if err := value.set(field, item.isNull, item.data, fieldSourceClient); err != nil {
			return nil, err
		}
	}

	return value, nil
}

// 判断字段是否提交
func (value *Mutable[E]) Has(field string) bool {
	_, exists := value.get(field)

	return exists
}

// 判断字段是否显式 null
func (value *Mutable[E]) IsNull(field string) bool {
	item, exists := value.get(field)

	return exists && item.isNull
}

// 读取字段值
func (value *Mutable[E]) Get(field string) (any, bool) {
	item, exists := value.get(field)
	if !exists || item.isNull {
		return nil, exists
	}

	return cloneData(item.data), true
}

// 设置字段值
func (value *Mutable[E]) Set(field string, data any) error {
	if value == nil || value.descriptor == nil {
		return exception.Validate("可写字段集合无效")
	}
	item, exists := value.descriptor.JSON(field)
	if !exists {
		return exception.Validate(fmt.Sprintf("实体字段 %s 不存在", field))
	}

	return value.set(item, false, data, fieldSourceServer)
}

// 将字段设为 null
func (value *Mutable[E]) SetNull(field string) error {
	if value == nil || value.descriptor == nil {
		return exception.Validate("可写字段集合无效")
	}
	item, exists := value.descriptor.JSON(field)
	if !exists {
		return exception.Validate(fmt.Sprintf("实体字段 %s 不存在", field))
	}

	return value.set(item, true, nil, fieldSourceServer)
}

// 删除字段及其提交状态
func (value *Mutable[E]) Unset(field string) error {
	if value == nil || value.descriptor == nil {
		return exception.Validate("可写字段集合无效")
	}
	item, exists := value.descriptor.JSON(field)
	if !exists {
		return exception.Validate(fmt.Sprintf("实体字段 %s 不存在", field))
	}
	delete(value.values, item.Name())

	return nil
}

// 判断是否为数组输入
func (in AddInput[E]) IsMany() bool { return in.isMany }

// 读取单对象输入
func (in AddInput[E]) One() *Mutable[E] {
	if in.isMany {
		return nil
	}

	return in.one
}

// 读取数组输入
func (in AddInput[E]) Many() []*Mutable[E] {
	if !in.isMany {
		return nil
	}

	return append([]*Mutable[E](nil), in.many...)
}

// 构造单对象新增输入
func NewAddObject[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	value *Mutable[E],
) (AddInput[E], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return AddInput[E]{}, err
	}
	if err := validateMutable[E, ID](descriptor, value); err != nil {
		return AddInput[E]{}, err
	}

	return AddInput[E]{one: value}, nil
}

// 构造数组新增输入
func NewAddArray[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	values []*Mutable[E],
) (AddInput[E], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return AddInput[E]{}, err
	}
	if len(values) == 0 {
		return AddInput[E]{}, exception.Validate("新增数组不能为空")
	}
	for _, value := range values {
		if err := validateMutable[E, ID](descriptor, value); err != nil {
			return AddInput[E]{}, err
		}
	}

	return AddInput[E]{isMany: true, many: append([]*Mutable[E](nil), values...)}, nil
}

// 构造删除输入
func NewDeleteInput[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	ids []ID,
) (DeleteInput[ID], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return DeleteInput[ID]{}, err
	}
	if len(ids) == 0 {
		return DeleteInput[ID]{}, exception.Validate("删除 ID 不能为空")
	}
	unique := make([]ID, 0, len(ids))
	seen := make(map[ID]struct{}, len(ids))
	for _, id := range ids {
		if err := validateID[E](descriptor, id); err != nil {
			return DeleteInput[ID]{}, err
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	return DeleteInput[ID]{ids: unique}, nil
}

// 读取删除 ID
func (in DeleteInput[ID]) IDs() []ID {
	return append([]ID(nil), in.ids...)
}

// 构造单项更新输入
func NewUpdateItem[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	id ID,
	value *Mutable[E],
) (UpdateItem[E, ID], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return UpdateItem[E, ID]{}, err
	}
	if err := validateID[E](descriptor, id); err != nil {
		return UpdateItem[E, ID]{}, err
	}
	if err := validateMutable[E, ID](descriptor, value); err != nil {
		return UpdateItem[E, ID]{}, err
	}

	return UpdateItem[E, ID]{id: id, mutable: value}, nil
}

// 构造单对象更新输入
func NewUpdateObject[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	item UpdateItem[E, ID],
) (UpdateInput[E, ID], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return UpdateInput[E, ID]{}, err
	}
	if err := validateUpdateItem(descriptor, item); err != nil {
		return UpdateInput[E, ID]{}, err
	}

	return UpdateInput[E, ID]{one: item}, nil
}

// 构造数组更新输入
func NewUpdateArray[E any, ID comparable](
	descriptor gnentity.Descriptor[E, ID],
	items []UpdateItem[E, ID],
) (UpdateInput[E, ID], error) {
	if err := validateDescriptor[E, ID](descriptor); err != nil {
		return UpdateInput[E, ID]{}, err
	}
	if len(items) == 0 {
		return UpdateInput[E, ID]{}, exception.Validate("更新数组不能为空")
	}
	for _, item := range items {
		if err := validateUpdateItem(descriptor, item); err != nil {
			return UpdateInput[E, ID]{}, err
		}
	}

	return UpdateInput[E, ID]{isMany: true, many: append([]UpdateItem[E, ID](nil), items...)}, nil
}

// 判断是否为数组输入
func (in UpdateInput[E, ID]) IsMany() bool { return in.isMany }

// 读取单对象输入
func (in UpdateInput[E, ID]) One() UpdateItem[E, ID] {
	if in.isMany {
		return UpdateItem[E, ID]{}
	}

	return in.one
}

// 读取数组输入
func (in UpdateInput[E, ID]) Many() []UpdateItem[E, ID] {
	if !in.isMany {
		return nil
	}

	return append([]UpdateItem[E, ID](nil), in.many...)
}

// 读取更新 ID
func (item UpdateItem[E, ID]) ID() ID { return item.id }

// 读取可写字段集合
func (item UpdateItem[E, ID]) Mutable() *Mutable[E] { return item.mutable }

// 判断是否为数组结果
func (result AddResult[ID]) IsMany() bool { return result.isMany }

// 读取单个新增 ID
func (result AddResult[ID]) One() ID {
	if result.isMany {
		var zero ID

		return zero
	}

	return result.one
}

// 读取多个新增 ID
func (result AddResult[ID]) Many() []ID {
	if !result.isMany {
		return nil
	}

	return append([]ID(nil), result.many...)
}

// 编码新增结果
func (result AddResult[ID]) MarshalJSON() ([]byte, error) {
	if result.isMany {
		return json.Marshal(struct {
			ID []ID `json:"id"`
		}{ID: result.Many()})
	}

	return json.Marshal(struct {
		ID ID `json:"id"`
	}{ID: result.one})
}

// 构造查询参数
func NewQuery(request *crud.QueryRequest, page, size int) (Query, error) {
	if page <= 0 || size <= 0 {
		return Query{}, exception.Validate("分页参数必须为正数")
	}

	return Query{request: request, page: page, size: size}, nil
}

// 构造带硬上限的列表查询参数
func NewListQuery(request *crud.QueryRequest, limit int) (Query, error) {
	if limit <= 0 {
		return Query{}, exception.Validate("列表上限必须为正数")
	}

	return Query{request: request, page: 1, size: 1, listLimit: limit}, nil
}

// 读取查询请求
func (query Query) Request() *QueryRequest { return query.request }

// 读取页码
func (query Query) PageNumber() int { return query.page }

// 读取每页数量
func (query Query) PageSize() int { return query.size }

// 读取列表硬上限
func (query Query) ListLimit() int { return query.listLimit }

// 读取记录字段
func (record Record) Get(field string) (any, bool) {
	value, exists := record.values[field]
	if !exists {
		return nil, false
	}

	return cloneData(value), true
}

// 扫描记录到目标对象
func (record Record) Scan(pointer any) error {
	encoded, err := record.MarshalJSON()
	if err != nil {
		return exception.WrapCore(err, "编码记录失败")
	}
	if err := json.Unmarshal(encoded, pointer); err != nil {
		return exception.WrapCore(err, "扫描记录失败")
	}

	return nil
}

// 编码记录
func (record Record) MarshalJSON() ([]byte, error) {
	if record.values == nil {
		return []byte("{}"), nil
	}

	return json.Marshal(record.values)
}

// 读取字段状态
func (value *Mutable[E]) get(name string) (mutableValue, bool) {
	if value == nil || value.descriptor == nil {
		return mutableValue{}, false
	}
	field, exists := value.descriptor.JSON(name)
	if !exists {
		return mutableValue{}, false
	}
	item, exists := value.values[field.Name()]

	return item, exists
}

// 校验并写入字段
func (value *Mutable[E]) set(field gnentity.Field, isNull bool, data any, source fieldSource) error {
	if isNull {
		if !field.Nullable() {
			return exception.Validate(fmt.Sprintf("实体字段 %s 不允许为 null", field.Name()))
		}
		value.values[field.Name()] = mutableValue{source: source, isNull: true}

		return nil
	}
	if data == nil || isNil(data) {
		return exception.Validate(fmt.Sprintf("实体字段 %s 的 nil 值必须使用 Null", field.Name()))
	}
	expected := field.GoType()
	if expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	if reflect.TypeOf(data) != expected {
		return exception.Validate(fmt.Sprintf(
			"实体字段 %s 类型错误，期望 %s，实际 %s",
			field.Name(),
			expected,
			reflect.TypeOf(data),
		))
	}
	value.values[field.Name()] = mutableValue{source: source, data: cloneData(data)}

	return nil
}

// 校验 Descriptor 类型绑定
func validateDescriptor[E any, ID comparable](descriptor gnentity.Descriptor[E, ID]) error {
	if isNil(descriptor) {
		return exception.Validate("实体 Descriptor 不能为空")
	}
	if descriptor.EntityType() != reflect.TypeFor[E]() || descriptor.IDType() != reflect.TypeFor[ID]() {
		return exception.Validate("实体 Descriptor 类型不匹配")
	}
	primary := descriptor.Primary()
	if primary == nil || !primary.Primary() || primary.GoType() != descriptor.IDType() {
		return exception.Validate("实体 Descriptor 主键无效")
	}

	return nil
}

// 校验可写字段集合所属实体
func validateMutable[E any, ID comparable](descriptor gnentity.Descriptor[E, ID], value *Mutable[E]) error {
	if value == nil || value.descriptor == nil || value.entityType != descriptor.EntityType() ||
		value.idType != descriptor.IDType() || value.descriptor.Table() != descriptor.Table() {
		return exception.Validate("可写字段集合与实体 Descriptor 不匹配")
	}

	return nil
}

// 校验主键类型
func validateID[E any, ID comparable](descriptor gnentity.Descriptor[E, ID], id ID) error {
	if reflect.TypeOf(id) != descriptor.IDType() {
		return exception.Validate(fmt.Sprintf("实体主键类型错误，期望 %s", descriptor.IDType()))
	}

	return nil
}

// 校验更新项
func validateUpdateItem[E any, ID comparable](descriptor gnentity.Descriptor[E, ID], item UpdateItem[E, ID]) error {
	if err := validateID[E](descriptor, item.id); err != nil {
		return err
	}

	return validateMutable[E, ID](descriptor, item.mutable)
}

// 判断接口值是否为 nil
func isNil(value any) bool {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return true
	}
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// 复制切片值
func cloneData(value any) any {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	switch reflected.Kind() {
	case reflect.Slice:
		if reflected.IsNil() {
			return reflect.Zero(reflected.Type()).Interface()
		}
		cloned := reflect.MakeSlice(reflected.Type(), reflected.Len(), reflected.Len())
		reflect.Copy(cloned, reflected)
		return cloned.Interface()
	default:
		return value
	}
}
