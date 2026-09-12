package config

import (
	"context"
	"fmt"
	"os"
	"reflect"

	"github.com/gogf/gf/v2/util/gvalid"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 按名称查询环境变量
type LookupEnv func(name string) (value string, found bool)

// 主配置和环境变量来源
type Source struct {
	Main      []byte    // 主配置文件内容
	LookupEnv LookupEnv // 环境变量查找函数
}

// 不可变的强类型配置结果
type Result[T any] struct {
	value     T
	canonical []byte
}

// 合并并校验配置
func Load[T any](ctx context.Context, defaults T, source Source) (*Result[T], error) {
	schema, err := buildSchema(reflect.TypeOf(defaults))
	if err != nil {
		return nil, exception.WrapCore(err, "配置结构定义无效: "+err.Error())
	}
	defaultNode, err := nodeFromValue(reflect.ValueOf(defaults), schema)
	if err != nil {
		return nil, exception.WrapCore(err, "代码默认配置无效: "+err.Error())
	}
	mainContent := append([]byte(nil), source.Main...)
	mainNode, err := parseMain(mainContent, schema, source.LookupEnv)
	if err != nil {
		return nil, exception.WrapCore(err, "主配置无效: "+err.Error())
	}
	mergedNode := mergeNodes(defaultNode, mainNode)
	decoded, err := decodeNode(mergedNode)
	if err != nil {
		return nil, exception.WrapCore(err, "配置解码失败: "+err.Error())
	}
	value := decoded.Interface().(T)
	if validationErr := gvalid.New().Data(value).Run(ctx); validationErr != nil {
		message := "配置校验失败"
		if field, _ := validationErr.FirstItem(); field != "" {
			message += ": " + fieldPath(schema, field)
		}
		return nil, exception.WrapCore(validationErr, message)
	}
	canonical, err := encodeCanonical(mergedNode)
	if err != nil {
		return nil, exception.WrapCore(err, "配置规范化失败: "+err.Error())
	}

	return &Result[T]{value: value, canonical: canonical}, nil
}

// 从明确路径读取主配置并执行加载
func LoadFile[T any](ctx context.Context, defaults T, path string, lookupEnv LookupEnv) (*Result[T], error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, exception.WrapCore(err, "主配置文件读取失败")
	}

	return Load(ctx, defaults, Source{Main: content, LookupEnv: lookupEnv})
}

// 保留原始错误链的错误类型
type preservedCause struct {
	message string
	cause   error
}

// 当前错误描述
func (e *preservedCause) Error() string {
	return e.message
}

// 解包后的原始错误
func (e *preservedCause) Unwrap() error {
	return e.cause
}

// 创建保留原始错误链的错误
func preserveCause(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s", message)
	}

	return &preservedCause{message: message, cause: cause}
}

// 返回配置值的深拷贝
func (r *Result[T]) Value() T {
	if r == nil {
		var zero T
		return zero
	}

	return clone(r.value)
}

// 返回规范化配置 JSON 的字节副本
func (r *Result[T]) CanonicalJSON() []byte {
	if r == nil {
		return nil
	}

	return append([]byte(nil), r.canonical...)
}

// 值深拷贝
func clone[T any](value T) T {
	cloned := cloneValue(reflect.ValueOf(value))
	if !cloned.IsValid() {
		var zero T
		return zero
	}

	return cloned.Interface().(T)
}

// reflect.Value 深拷贝
func cloneValue(value reflect.Value) reflect.Value {
	return cloneSeen(value, make(map[cloneVisit]reflect.Value))
}

// 深拷贝循环引用去重 key
type cloneVisit struct {
	typ      reflect.Type
	pointer  uintptr
	length   int
	capacity int
}

func cloneSeen(value reflect.Value, seen map[cloneVisit]reflect.Value) reflect.Value {
	if !value.IsValid() {
		return reflect.Value{}
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{typ: value.Type(), pointer: value.Pointer()}
		if cloned, exists := seen[visit]; exists {
			return cloned
		}
		cloned := reflect.New(value.Type().Elem())
		seen[visit] = cloned
		cloned.Elem().Set(cloneSeen(value.Elem(), seen))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{typ: value.Type(), pointer: value.Pointer()}
		if cloned, exists := seen[visit]; exists {
			return cloned
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		seen[visit] = cloned
		iterator := value.MapRange()
		for iterator.Next() {
			cloned.SetMapIndex(
				cloneSeen(iterator.Key(), seen),
				cloneSeen(iterator.Value(), seen),
			)
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{
			typ:      value.Type(),
			pointer:  value.Pointer(),
			length:   value.Len(),
			capacity: value.Cap(),
		}
		if cloned, exists := seen[visit]; exists {
			return cloned
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Cap())
		seen[visit] = cloned
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(cloneSeen(value.Index(index), seen))
		}
		return cloned
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneSeen(value.Elem(), seen))
		return cloned
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(cloneSeen(value.Index(index), seen))
		}
		return cloned
	case reflect.Struct:
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(value)
		for index := 0; index < value.NumField(); index++ {
			if cloned.Field(index).CanSet() && value.Field(index).CanInterface() {
				cloned.Field(index).Set(cloneSeen(value.Field(index), seen))
			}
		}
		return cloned
	default:
		return value
	}
}
