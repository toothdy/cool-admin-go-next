package gnctrl

import (
	"context"
	"reflect"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
)

// 默认 CRUD 绑定前回调
type BeforeFunc func(context.Context) error

// 新增字段注入器
type InsertParam interface {
	insertParam()
}

type insertParamValue[E any] struct {
	entityType reflect.Type
	apply      func(context.Context, *gnservice.Mutable[E]) error
}

func (insertParamValue[E]) insertParam() {}

func (value insertParamValue[E]) insertEntityType() reflect.Type { return value.entityType }

type typedInsertParam interface {
	insertEntityType() reflect.Type
}

// 强类型新增字段注入器
func Insert[E any](apply func(context.Context, *gnservice.Mutable[E]) error) InsertParam {
	if apply == nil {
		panicCore("InsertParam 函数不能为空")
	}
	entityType := reflect.TypeFor[E]()
	if entityType.Kind() != reflect.Struct || entityType.Name() == "" || entityType.PkgPath() == "" {
		panicCore("InsertParam 实体必须是非指针具名 struct")
	}

	return insertParamValue[E]{entityType: entityType, apply: apply}
}

// 默认 CRUD 前置回调
func ApplyBefore(ctx context.Context, value Definition) error {
	if isNilValue(ctx) {
		return exception.Core("Controller 上下文不能为空")
	}
	option, err := requireCurd(value)
	if err != nil {
		return err
	}
	if option.Before == nil {
		return nil
	}

	return option.Before(ctx)
}

// 顺序执行新增字段注入
func ApplyInsertParam[E any](ctx context.Context, value Definition, inputs []*gnservice.Mutable[E]) error {
	if isNilValue(ctx) {
		return exception.Core("Controller 上下文不能为空")
	}
	option, err := requireCurd(value)
	if err != nil {
		return err
	}
	if option.InsertParam == nil {
		return nil
	}
	typed, matches := option.InsertParam.(typedInsertParam)
	if !matches || typed.insertEntityType() != reflect.TypeFor[E]() {
		return exception.Core("InsertParam 实体类型不匹配")
	}
	current, matches := option.InsertParam.(insertParamValue[E])
	if !matches {
		return exception.Core("InsertParam 实体类型不匹配")
	}
	for _, input := range inputs {
		if input == nil {
			return exception.Core("InsertParam 输入不能为空")
		}
		if err = current.apply(ctx, input); err != nil {
			return err
		}
	}

	return nil
}

func requireInsertParam(value InsertParam) {
	if typed, matches := value.(typedInsertParam); !matches || typed.insertEntityType() == nil {
		panicCore("InsertParam 无效")
	}
}
