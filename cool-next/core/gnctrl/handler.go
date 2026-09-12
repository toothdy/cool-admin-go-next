package gnctrl

import (
	"context"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"
)

// 生成 Handler 使用的 GoFrame 空请求类型
type HTTPRequest struct{}

// 已绑定请求的自定义路由调用
type HTTPRouteInvoke func(context.Context) (any, error)

// 带 DTO 的自定义路由调用
type HTTPDTOInvoke[T any] func(context.Context, *T) (any, error)

// 生成路由可安装的业务中间件
type HTTPMiddleware interface {
	Handle(*ghttp.Request)
}

// 静态 Descriptor 适配为 Resolver 的函数类型
type DescriptorResolverFunc func(any) (gnentity.Metadata, bool)

// 实体对应的静态 Descriptor
func (resolve DescriptorResolverFunc) Resolve(value any) (gnentity.Metadata, bool) {
	if resolve == nil {
		return nil, false
	}

	return resolve(value)
}

// 新增输入并执行生成期选定的 Service Adapter
func HandleAdd[E any, ID comparable](
	ctx context.Context,
	binder *Binder,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	mode crud.ActionMode,
	descriptor gnentity.Descriptor[E, ID],
	invoke func(context.Context, gnservice.AddInput[E]) (gnservice.AddResult[ID], error),
) (any, error) {
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	var (
		input  gnservice.AddInput[E]
		result gnservice.AddResult[ID]
	)
	err := HandleCRUD(
		ctx,
		definition,
		crud.ActionAdd,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}
			input, requestErr = BindAdd[E, ID](binder, request, descriptor)

			return nil, requestErr
		},
		func(scopeCtx context.Context) error {
			values := input.Many()
			if !input.IsMany() {
				values = []*gnservice.Mutable[E]{input.One()}
			}

			return ApplyInsertParam(scopeCtx, definition, values)
		},
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, crud.ActionAdd, request)
		},
		func(scopeCtx context.Context) error {
			var invokeErr error
			result, invokeErr = invoke(scopeCtx, input)

			return invokeErr
		},
	)

	return result, err
}

// 删除输入并执行生成期选定的 Service Adapter
func HandleDelete[E any, ID comparable](
	ctx context.Context,
	binder *Binder,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	mode crud.ActionMode,
	descriptor gnentity.Descriptor[E, ID],
	invoke func(context.Context, gnservice.DeleteInput[ID]) error,
) (any, error) {
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	var input gnservice.DeleteInput[ID]
	err := HandleCRUD(
		ctx,
		definition,
		crud.ActionDelete,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}
			input, requestErr = BindDelete[E, ID](binder, request, descriptor)

			return nil, requestErr
		},
		nil,
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, crud.ActionDelete, request)
		},
		func(scopeCtx context.Context) error { return invoke(scopeCtx, input) },
	)

	return nil, err
}

// 更新输入并执行生成期选定的 Service Adapter
func HandleUpdate[E any, ID comparable](
	ctx context.Context,
	binder *Binder,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	mode crud.ActionMode,
	descriptor gnentity.Descriptor[E, ID],
	invoke func(context.Context, gnservice.UpdateInput[E, ID]) error,
) (any, error) {
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	var input gnservice.UpdateInput[E, ID]
	err := HandleCRUD(
		ctx,
		definition,
		crud.ActionUpdate,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}
			input, requestErr = BindUpdate[E, ID](binder, request, descriptor)

			return nil, requestErr
		},
		nil,
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, crud.ActionUpdate, request)
		},
		func(scopeCtx context.Context) error { return invoke(scopeCtx, input) },
	)

	return nil, err
}

// 详情主键并执行生成期选定的 Service Adapter
func HandleInfo[ID comparable](
	ctx context.Context,
	binder *Binder,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	mode crud.ActionMode,
	invoke func(context.Context, ID) (any, error),
) (any, error) {
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	var (
		input  ID
		result any
	)
	err := HandleCRUD(
		ctx,
		definition,
		crud.ActionInfo,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}
			input, requestErr = BindInfo[ID](binder, request)

			return nil, requestErr
		},
		nil,
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, crud.ActionInfo, request)
		},
		func(scopeCtx context.Context) error {
			var invokeErr error
			result, invokeErr = invoke(scopeCtx, input)

			return invokeErr
		},
	)

	return result, err
}

// List 或 Page 输入并执行生成期选定的 Service Adapter
func HandleQuery(
	ctx context.Context,
	binder *Binder,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	action crud.Action,
	mode crud.ActionMode,
	invoke func(context.Context, gnservice.Query) (any, error),
) (any, error) {
	if action != crud.ActionList && action != crud.ActionPage {
		return nil, exception.Core("HTTP 查询动作必须是 list 或 page")
	}
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	var (
		input  gnservice.Query
		result any
	)
	err := HandleCRUD(
		ctx,
		definition,
		action,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}
			input, requestErr = BindCRUDQuery(binder, request, action)
			if requestErr != nil {
				return nil, requestErr
			}

			return input.Request(), nil
		},
		nil,
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, action, request)
		},
		func(scopeCtx context.Context) error {
			var invokeErr error
			result, invokeErr = invoke(scopeCtx, input)

			return invokeErr
		},
	)

	return result, err
}

// 自定义查询 DTO 并执行对应 CRUD Handler
func HandleCRUDDTO[T any](
	ctx context.Context,
	binder *Binder,
	source BindSource,
	dispatcher *crud.Dispatcher,
	resolver crud.DescriptorResolver,
	definition Definition,
	action crud.Action,
	mode crud.ActionMode,
	invoke HTTPDTOInvoke[T],
) (any, error) {
	if err := requireHTTPCRUD(ctx, binder, dispatcher, resolver, invoke); err != nil {
		return nil, err
	}
	input := new(T)
	var result any
	err := HandleCRUD(
		ctx,
		definition,
		action,
		mode,
		dispatcher,
		func(scopeCtx context.Context) (*crud.QueryRequest, error) {
			request, requestErr := httpRequest(scopeCtx)
			if requestErr != nil {
				return nil, requestErr
			}

			return nil, binder.BindDTO(request, source, input)
		},
		nil,
		func(scopeCtx context.Context, request *crud.QueryRequest) (*crud.ActionPlan, error) {
			return CompilePlan(scopeCtx, resolver, definition, action, request)
		},
		func(scopeCtx context.Context) error {
			var invokeErr error
			result, invokeErr = invoke(scopeCtx, input)

			return invokeErr
		},
	)

	return result, err
}

// DTO 并按声明的事务策略调用 Handler
func HandleDTO[T any](
	ctx context.Context,
	binder *Binder,
	source BindSource,
	runner tx.Runner,
	policy TransactionPolicy,
	invoke HTTPDTOInvoke[T],
) (any, error) {
	if binder == nil {
		return nil, exception.Core("HTTP Binder 未初始化")
	}
	if invoke == nil {
		return nil, exception.Core("HTTP DTO Handler 不能为空")
	}
	request, err := httpRequest(ctx)
	if err != nil {
		return nil, err
	}
	input := new(T)
	if err = binder.BindDTO(request, source, input); err != nil {
		return nil, err
	}

	return handleRouteValue(ctx, runner, policy, func(scopeCtx context.Context) (any, error) {
		return invoke(scopeCtx, input)
	})
}

// 依事务策略调用无 DTO Handler
func HandleNoDTO(
	ctx context.Context,
	runner tx.Runner,
	policy TransactionPolicy,
	invoke HTTPRouteInvoke,
) (any, error) {
	if invoke == nil {
		return nil, exception.Core("HTTP Handler 不能为空")
	}
	if _, err := httpRequest(ctx); err != nil {
		return nil, err
	}

	return handleRouteValue(ctx, runner, policy, invoke)
}

func handleRouteValue(
	ctx context.Context,
	runner tx.Runner,
	policy TransactionPolicy,
	invoke HTTPRouteInvoke,
) (any, error) {
	var result any
	err := HandleRoute(ctx, runner, policy, func(scopeCtx context.Context) error {
		var invokeErr error
		result, invokeErr = invoke(scopeCtx)

		return invokeErr
	})

	return result, err
}

func requireHTTPCRUD(ctx context.Context, binder *Binder, dispatcher *crud.Dispatcher, resolver crud.DescriptorResolver, invoke any) error {
	if _, err := httpRequest(ctx); err != nil {
		return err
	}
	if binder == nil {
		return exception.Core("HTTP Binder 未初始化")
	}
	if dispatcher == nil {
		return exception.Core("CRUD Dispatcher 未初始化")
	}
	if isNilValue(resolver) {
		return exception.Core("Descriptor 解析器不能为空")
	}
	if isNilValue(invoke) {
		return exception.Core("HTTP CRUD Handler 不能为空")
	}

	return nil
}

func httpRequest(ctx context.Context) (*ghttp.Request, error) {
	if ctx == nil {
		return nil, exception.Core("HTTP 请求上下文无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request := ghttp.RequestFromCtx(ctx)
	if request == nil {
		return nil, exception.Core("HTTP 请求上下文无效")
	}

	return request, nil
}
