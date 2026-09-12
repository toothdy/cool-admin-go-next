package crud

import (
	"fmt"
	"reflect"
	"regexp"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

var queryNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// 单个查询请求字段
type RequestValue struct {
	isNull      bool
	isSubmitted bool
	name        string
	value       any
}

// Presence-aware 查询请求
type QueryRequest struct {
	values map[string]RequestValue
}

// 构造普通查询请求字段
func RequestField(name string, value any) RequestValue {
	return RequestValue{
		isSubmitted: true,
		name:        name,
		value:       cloneRequestData(value),
	}
}

// 构造显式 null 查询请求字段
func RequestNull(name string) RequestValue {
	return RequestValue{
		isNull:      true,
		isSubmitted: true,
		name:        name,
	}
}

// 构造不可变查询请求
func NewQueryRequest(values []RequestValue) (*QueryRequest, error) {
	request := &QueryRequest{values: make(map[string]RequestValue, len(values))}
	for _, item := range values {
		if !item.isSubmitted {
			return nil, exception.Validate("查询请求字段无效")
		}
		if !queryNamePattern.MatchString(item.name) {
			return nil, exception.Validate(fmt.Sprintf("查询请求字段名 %q 无效", item.name))
		}
		if !item.isNull && item.value == nil {
			return nil, exception.Validate(fmt.Sprintf("查询请求字段 %s 的 nil 值必须使用 RequestNull", item.name))
		}
		if _, exists := request.values[item.name]; exists {
			return nil, exception.Validate(fmt.Sprintf("查询请求字段 %s 重复", item.name))
		}
		item.value = cloneRequestData(item.value)
		request.values[item.name] = item
	}

	return request, nil
}

// 请求字段是否提交
func (request *QueryRequest) Has(name string) bool {
	if request == nil {
		return false
	}
	_, exists := request.values[name]

	return exists
}

// 查询字段值
func (request *QueryRequest) Value(name string) (any, bool) {
	if request == nil {
		return nil, false
	}
	value, exists := request.values[name]
	if !exists {
		return nil, false
	}
	if value.isNull {
		return nil, true
	}

	return cloneRequestData(value.value), true
}

// 字符串查询字段
func (request *QueryRequest) String(name string) (string, bool) {
	value, exists := request.Value(name)
	if !exists || value == nil {
		return "", false
	}
	result, matches := value.(string)

	return result, matches
}

// 布尔查询字段
func (request *QueryRequest) Bool(name string) (bool, bool) {
	value, exists := request.Value(name)
	if !exists || value == nil {
		return false, false
	}
	result, matches := value.(bool)

	return result, matches
}

// 切片(独立副本)
func (request *QueryRequest) Strings(name string) ([]string, bool) {
	value, exists := request.Value(name)
	if !exists || value == nil {
		return nil, false
	}
	result, matches := value.([]string)
	if !matches {
		return nil, false
	}

	return append([]string(nil), result...), true
}

// 复制请求中的切片或数组
func cloneRequestData(value any) any {
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
	case reflect.Array:
		cloned := reflect.New(reflected.Type()).Elem()
		reflect.Copy(cloned, reflected)
		return cloned.Interface()
	default:
		return value
	}
}
