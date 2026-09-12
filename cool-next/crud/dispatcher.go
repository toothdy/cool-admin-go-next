package crud

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"

	"fmt"
)

// CRUD 动作执行模式
type ActionMode string

const (
	ActionModeBase     ActionMode = "base"     // 基础实现
	ActionModeOverride ActionMode = "override" // 完全覆盖
	ActionModeDelegate ActionMode = "delegate" // 委托基础实现
)

// 生成期选定的 CRUD 调用入口
type Adapter func(context.Context) error

// 协议无关的 CRUD 事务调度器
type Dispatcher struct {
	runner tx.Runner
}

// 单次 CRUD 调度状态
type DispatchScope struct {
	action Action
	mode   ActionMode
}

type dispatchContextKey struct{}

// CRUD Dispatcher
func NewDispatcher(runner tx.Runner) (*Dispatcher, error) {
	if isNilPlanValue(runner) {
		return nil, exception.Core("CRUD Dispatcher 的事务 Runner 不能为空")
	}

	return &Dispatcher{runner: runner}, nil
}

// 在框架事务中调用生成期选定的 Adapter
func (dispatcher *Dispatcher) Dispatch(
	ctx context.Context,
	action Action,
	mode ActionMode,
	plan *ActionPlan,
	adapter Adapter,
) error {
	if dispatcher == nil || isNilPlanValue(dispatcher.runner) {
		return exception.Core("CRUD Dispatcher 未初始化")
	}
	if ctx == nil {
		return exception.Core("CRUD Dispatcher 上下文不能为空")
	}
	if !isAction(action) {
		return exception.Core("CRUD 动作无效")
	}
	if adapter == nil {
		return exception.Core("CRUD Adapter 不能为空")
	}
	if err := checkDispatch(action, mode, plan); err != nil {
		return err
	}

	return dispatcher.runner.Within(ctx, func(scopeCtx context.Context) error {
		scopeCtx = context.WithValue(scopeCtx, dispatchContextKey{}, &DispatchScope{action: action, mode: mode})
		scopeCtx = WithOperation(scopeCtx, plan)

		return adapter(scopeCtx)
	})
}

// 返回当前调度上下文
func CurrentDispatch(ctx context.Context) (*DispatchScope, bool) {
	if ctx == nil {
		return nil, false
	}
	scope, exists := ctx.Value(dispatchContextKey{}).(*DispatchScope)
	if !exists || scope == nil || !isAction(scope.action) || !isActionMode(scope.mode) {
		return nil, false
	}

	return scope, true
}

// CRUD 动作
func (scope *DispatchScope) Action() Action {
	if scope == nil {
		return ""
	}

	return scope.action
}

// CRUD 动作模式
func (scope *DispatchScope) Mode() ActionMode {
	if scope == nil {
		return ""
	}

	return scope.mode
}

func checkDispatch(action Action, mode ActionMode, plan *ActionPlan) error {
	if !isActionMode(mode) {
		return exception.Core("CRUD 动作模式无效")
	}
	if plan == nil {
		return exception.Core(fmt.Sprintf("%s 模式必须提供 CRUD 动作计划", mode))
	}
	if plan.Action() != action {
		return exception.Core(fmt.Sprintf("CRUD 动作计划不匹配: 当前 %s，请求 %s", plan.Action(), action))
	}

	return nil
}

func isActionMode(mode ActionMode) bool {
	switch mode {
	case ActionModeBase, ActionModeOverride, ActionModeDelegate:
		return true
	default:
		return false
	}
}
