package gnctrl

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"
)

// CRUD 请求绑定阶段
type CRUDBindFunc func(context.Context) (*crud.QueryRequest, error)

// Base 专用请求增强阶段
type CRUDEnhanceFunc func(context.Context) error

// CRUD 动作计划编译阶段
type CRUDPlanFunc func(context.Context, *crud.QueryRequest) (*crud.ActionPlan, error)

// CRUD Service 调用阶段
type CRUDInvokeFunc func(context.Context) error

// 自定义 Route 调用阶段
type RouteInvokeFunc func(context.Context) error

// 按生成期模式执行 CRUD 请求处理链
func HandleCRUD(
	ctx context.Context,
	definition Definition,
	action crud.Action,
	mode crud.ActionMode,
	dispatcher *crud.Dispatcher,
	bind CRUDBindFunc,
	enhance CRUDEnhanceFunc,
	compile CRUDPlanFunc,
	invoke CRUDInvokeFunc,
) error {
	if ctx == nil {
		return exception.Core("CRUD 请求上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if bind == nil {
		return exception.Core("CRUD 请求绑定函数不能为空")
	}
	if invoke == nil {
		return exception.Core("CRUD Service 调用函数不能为空")
	}
	switch mode {
	case crud.ActionModeBase, crud.ActionModeDelegate, crud.ActionModeOverride:
	default:
		return exception.Core("CRUD 动作模式无效")
	}
	if compile == nil {
		return exception.Core("CRUD 动作计划函数不能为空")
	}
	if err := ApplyBefore(ctx, definition); err != nil {
		return err
	}
	request, err := bind(ctx)
	if err != nil {
		return err
	}
	if enhance != nil {
		if err = enhance(ctx); err != nil {
			return err
		}
	}
	plan, err := compile(ctx, request)
	if err != nil {
		return err
	}

	return dispatcher.Dispatch(ctx, action, mode, plan, crud.Adapter(invoke))
}

// 按路由事务策略执行自定义 Handler
func HandleRoute(
	ctx context.Context,
	runner tx.Runner,
	policy TransactionPolicy,
	invoke RouteInvokeFunc,
) error {
	if ctx == nil {
		return exception.Core("自定义 Route 上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if invoke == nil {
		return exception.Core("自定义 Route Handler 不能为空")
	}
	if policy.IsNonTransactional() {
		return invoke(ctx)
	}
	if isNilValue(runner) {
		return exception.Core("自定义 Route 的事务 Runner 不能为空")
	}

	return runner.Within(ctx, tx.Callback(invoke))
}
