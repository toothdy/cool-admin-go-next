package gnctrl

import (
	"context"
	"fmt"
	"slices"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

// 默认 CRUD 动作计划
func CompilePlan(
	ctx context.Context,
	resolver crud.DescriptorResolver,
	value Definition,
	action crud.Action,
	request *crud.QueryRequest,
) (*crud.ActionPlan, error) {
	if isNilValue(ctx) {
		return nil, exception.Core("Controller 上下文不能为空")
	}
	if isNilValue(resolver) {
		return nil, exception.Core("Descriptor 解析器不能为空")
	}
	api, exists := actionAPI(action)
	if !exists {
		return nil, exception.Core("CRUD 动作无效")
	}
	option, err := requireCurd(value)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(option.API, api) {
		return nil, exception.Core(fmt.Sprintf("Controller 未启用 CRUD API %s", api))
	}

	query := QueryOp{}
	switch action {
	case crud.ActionPage:
		query, err = resolveProvider(ctx, option.PageQueryOp)
	case crud.ActionList:
		query, err = resolveProvider(ctx, option.ListQueryOp)
	}
	if err != nil {
		return nil, err
	}

	return crud.CompilePlan(ctx, resolver, crud.PlanInput{
		Action: action,
		Entity: option.Entity,
		Query:  query,
		Fields: crud.FieldPolicyInput{
			HiddenFields:       append([]ColumnRef(nil), option.HiddenFields...),
			ReadonlyFields:     append([]ColumnRef(nil), option.ReadonlyFields...),
			InfoIgnoreProperty: append([]ColumnRef(nil), option.InfoIgnoreProperty...),
			SortFields:         append([]ColumnRef(nil), option.SortFields...),
			DefaultSort:        option.DefaultSort,
			DefaultOrder:       option.DefaultOrder,
		},
	}, request)
}

func actionAPI(action crud.Action) (APIType, bool) {
	switch action {
	case crud.ActionAdd:
		return Add, true
	case crud.ActionDelete:
		return Delete, true
	case crud.ActionUpdate:
		return Update, true
	case crud.ActionInfo:
		return Info, true
	case crud.ActionList:
		return List, true
	case crud.ActionPage:
		return Page, true
	default:
		return "", false
	}
}
