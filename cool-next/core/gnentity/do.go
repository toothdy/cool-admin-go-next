package gnentity

import (
	"fmt"
	"reflect"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 不可变 DO 结构元数据
type doShape struct {
	entityType reflect.Type
	structType reflect.Type
	fields     map[string]doFieldBinding
	fieldCount int
}

// 逻辑字段到 DO struct 字段的不可变映射
type doFieldBinding struct {
	field       Field
	structIndex int
	stateIndex  int
	valueType   reflect.Type
}

type doFieldState uint8

const (
	doFieldUnset doFieldState = iota
	doFieldValue
	doFieldNull
)

// 单次写入的值对象,持有独立的状态和快照
type doValue struct {
	shape  *doShape
	data   reflect.Value
	states []doFieldState
}

// 编译 GoFrame 兼容的具体 DO struct 类型
func compileDOShape(entityType reflect.Type, table string, fields []Field) *doShape {
	structFields := []reflect.StructField{
		{
			Name:      "Meta",
			Type:      reflect.TypeFor[g.Meta](),
			Anonymous: true,
			Tag:       reflect.StructTag(fmt.Sprintf(`orm:"table:%s,do:true"`, table)),
		},
	}
	bindings := make(map[string]doFieldBinding, len(fields))
	for _, field := range fields {
		if !field.Persistent() {
			continue
		}
		index := len(bindings)
		structFields = append(structFields, reflect.StructField{
			Name: fmt.Sprintf("Field%d", index),
			Type: reflect.TypeFor[any](),
			Tag:  reflect.StructTag(fmt.Sprintf(`orm:"%s"`, field.Column())),
		})
		valueType := field.GoType()
		if valueType.Kind() == reflect.Pointer {
			valueType = valueType.Elem()
		}
		bindings[field.Name()] = doFieldBinding{
			field:       field,
			structIndex: index + 1,
			stateIndex:  index,
			valueType:   valueType,
		}
	}

	return &doShape{
		entityType: entityType,
		structType: reflect.StructOf(structFields),
		fields:     bindings,
		fieldCount: len(bindings),
	}
}

// 创建独立 DOValue 实例
func (s *doShape) newValue() DOValue {
	return &doValue{
		shape:  s,
		data:   reflect.New(s.structType).Elem(),
		states: make([]doFieldState, s.fieldCount),
	}
}

// 字段是否已提交
func (v *doValue) Has(field string) bool {
	binding, exists := v.shape.fields[field]

	return exists && v.states[binding.stateIndex] != doFieldUnset
}

// 字段是否显式设置为 null
func (v *doValue) IsNull(field string) bool {
	binding, exists := v.shape.fields[field]

	return exists && v.states[binding.stateIndex] == doFieldNull
}

// 设置字段值,严格校验类型与可空性
func (v *doValue) SetColumn(field string, value any) error {
	binding, exists := v.shape.fields[field]
	if !exists {
		return exception.Core(fmt.Sprintf("实体 %s 不存在逻辑字段 %s", v.shape.entityType, field))
	}
	if value == nil {
		return v.setNull(binding)
	}
	actualType := reflect.TypeOf(value)
	if isTypedNil(value) {
		if !binding.acceptsNilType(actualType) {
			return v.newTypeError(field, binding.valueType, actualType)
		}

		return v.setNull(binding)
	}
	if actualType != binding.valueType {
		return v.newTypeError(field, binding.valueType, actualType)
	}

	v.data.Field(binding.structIndex).Set(reflect.ValueOf(value))
	v.states[binding.stateIndex] = doFieldValue

	return nil
}

// 返回当前具体 DO struct 的值快照
func (v *doValue) DBData() any {
	return v.data.Interface()
}

// 将字段置为 SQL NULL（仅可空字段）
func (v *doValue) setNull(binding doFieldBinding) error {
	if !binding.field.Nullable() {
		return exception.Core(fmt.Sprintf("实体 %s 的逻辑字段 %s 不允许为 null",
			v.shape.entityType,
			binding.field.Name()),
		)
	}

	v.data.Field(binding.structIndex).Set(reflect.ValueOf(gdb.Raw("NULL")))
	v.states[binding.stateIndex] = doFieldNull

	return nil
}

// 构造不含字段值的类型错误
func (v *doValue) newTypeError(field string, expected, actual reflect.Type) error {
	return exception.Core(fmt.Sprintf("实体 %s 的逻辑字段 %s 类型错误，期望 %s，实际 %s",
		v.shape.entityType,
		field,
		expected,
		actual),
	)
}

// 判断值是否为空
func isTypedNil(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// 判断空值类型是否与字段声明兼容
func (b doFieldBinding) acceptsNilType(actual reflect.Type) bool {
	return actual == b.field.GoType() || actual == b.valueType
}
