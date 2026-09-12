package crud

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// CRUD 动作
type Action string

const (
	ActionAdd    Action = "add"    // 新增
	ActionDelete Action = "delete" // 删除
	ActionUpdate Action = "update" // 更新
	ActionInfo   Action = "info"   // 详情
	ActionList   Action = "list"   // 列表
	ActionPage   Action = "page"   // 分页
)

// 动作计划
type ActionPlan struct {
	action Action
	fields FieldPolicy
	query  *QueryPlan
}

// 字段策略编译输入
type FieldPolicyInput struct {
	HiddenFields       []ColumnRef
	ReadonlyFields     []ColumnRef
	InfoIgnoreProperty []ColumnRef
	SortFields         []ColumnRef
	DefaultSort        ColumnRef
	DefaultOrder       Direction
}

// 动作计划编译输入
type PlanInput struct {
	Action Action
	Entity any
	Query  QueryOp
	Fields FieldPolicyInput
}

// CRUD 字段策略
type FieldPolicy struct {
	hidden      map[string]struct{}
	infoIgnored map[string]struct{}
	readonly    map[string]struct{}
	sortable    map[string]planColumn
	defaultSort *planOrder
}

// 单次 CRUD 操作上下文
type OperationScope struct {
	plan *ActionPlan
}

type operationContextKey struct{}

// 编译动作计划
func CompilePlan(
	ctx context.Context,
	resolver DescriptorResolver,
	input PlanInput,
	request *QueryRequest,
) (*ActionPlan, error) {
	if !isAction(input.Action) {
		return nil, exception.Core("CRUD 动作无效")
	}
	autoSortFields := (input.Action == ActionList || input.Action == ActionPage) &&
		len(input.Fields.SortFields) == 0
	fields, err := compileFieldPolicy(resolver, input.Entity, input.Fields, autoSortFields)
	if err != nil {
		return nil, err
	}
	plan := &ActionPlan{action: input.Action, fields: fields}
	if !isQueryAction(input.Action) {
		return plan, nil
	}
	query, err := compileQueryPlan(ctx, resolver, input.Entity, input.Query, request)
	if err != nil {
		return nil, err
	}
	if err = applyFieldPolicy(input.Action, query, &plan.fields, request); err != nil {
		return nil, err
	}
	plan.query = query

	return plan, nil
}

// 字段策略
func (plan *ActionPlan) Fields() *FieldPolicy {
	if plan == nil {
		return nil
	}

	return &plan.fields
}

// 字段是否隐藏
func (policy *FieldPolicy) IsHidden(field string) bool {
	if policy == nil {
		return false
	}
	_, exists := policy.hidden[field]

	return exists
}

// 字段是否只读
func (policy *FieldPolicy) IsReadonly(field string) bool {
	if policy == nil {
		return false
	}
	_, exists := policy.readonly[field]

	return exists
}

// 详情是否忽略字段
func (policy *FieldPolicy) IsInfoIgnored(field string) bool {
	if policy == nil {
		return false
	}
	_, exists := policy.infoIgnored[field]

	return exists
}

// CRUD 动作
func (plan *ActionPlan) Action() Action {
	if plan == nil {
		return ""
	}

	return plan.action
}

// 查询计划
func (plan *ActionPlan) Query() *QueryPlan {
	if plan == nil {
		return nil
	}

	return plan.query
}

// 将查询计划应用到克隆后的 Model
func (plan *ActionPlan) ApplyQuery(ctx context.Context, model *gdb.Model) (*gdb.Model, error) {
	if plan == nil || plan.query == nil {
		return nil, exception.Core("动作计划不包含查询计划")
	}

	return applyQueryPlan(ctx, model, plan.query)
}

// 将动作计划写入派生 Context
func WithOperation(ctx context.Context, plan *ActionPlan) context.Context {
	if ctx == nil {
		panicCore("操作上下文不能为空")
	}
	if plan == nil || !isAction(plan.action) {
		panicCore("动作计划无效")
	}

	return context.WithValue(ctx, operationContextKey{}, &OperationScope{plan: plan})
}

// 返回当前操作上下文
func CurrentOperation(ctx context.Context) (*OperationScope, bool) {
	if ctx == nil {
		return nil, false
	}
	scope, exists := ctx.Value(operationContextKey{}).(*OperationScope)
	if !exists || scope == nil || scope.plan == nil {
		return nil, false
	}

	return scope, true
}

// 当前动作计划
func (scope *OperationScope) Plan() *ActionPlan {
	if scope == nil {
		return nil
	}

	return scope.plan
}

func isAction(action Action) bool {
	switch action {
	case ActionAdd, ActionDelete, ActionUpdate, ActionInfo, ActionList, ActionPage:
		return true
	default:
		return false
	}
}

func isQueryAction(action Action) bool {
	switch action {
	case ActionInfo, ActionList, ActionPage:
		return true
	default:
		return false
	}
}

func compileFieldPolicy(
	resolver DescriptorResolver,
	entityValue any,
	input FieldPolicyInput,
	autoSortFields bool,
) (FieldPolicy, error) {
	if emptyPolicy(input) && !autoSortFields {
		return FieldPolicy{}, nil
	}
	if isNilPlanValue(resolver) {
		return FieldPolicy{}, exception.Core("Descriptor 解析器不能为空")
	}
	if isNilPlanValue(entityValue) {
		return FieldPolicy{}, exception.Core("字段策略实体不能为空")
	}
	entityType := reflect.TypeOf(entityValue)
	if entityType.Kind() != reflect.Struct || entityType.Name() == "" || entityType.PkgPath() == "" {
		return FieldPolicy{}, exception.Core("字段策略实体类型无效")
	}
	metadata, exists := resolver.Resolve(entityValue)
	if !exists || isNilPlanValue(metadata) {
		return FieldPolicy{}, exception.Core("字段策略实体的 Descriptor 不存在")
	}
	policy := FieldPolicy{
		hidden:      make(map[string]struct{}, len(input.HiddenFields)),
		infoIgnored: make(map[string]struct{}, len(input.InfoIgnoreProperty)),
		readonly:    make(map[string]struct{}, len(input.ReadonlyFields)),
		sortable:    make(map[string]planColumn, len(input.SortFields)),
	}
	if err := compileFieldSet(metadata, entityType, input.HiddenFields, policy.hidden, "隐藏"); err != nil {
		return FieldPolicy{}, err
	}
	if err := compileFieldSet(metadata, entityType, input.ReadonlyFields, policy.readonly, "只读"); err != nil {
		return FieldPolicy{}, err
	}
	if err := compileFieldSet(metadata, entityType, input.InfoIgnoreProperty, policy.infoIgnored, "详情忽略"); err != nil {
		return FieldPolicy{}, err
	}
	if err := compileSortFields(metadata, entityType, input, &policy, autoSortFields); err != nil {
		return FieldPolicy{}, err
	}

	return policy, nil
}

func compileFieldSet(
	metadata gnentity.Metadata,
	entityType reflect.Type,
	fields []ColumnRef,
	target map[string]struct{},
	label string,
) error {
	for _, reference := range fields {
		field, err := resolvePolicyField(metadata, entityType, reference, label)
		if err != nil {
			return err
		}
		if _, exists := target[field.Name()]; exists {
			return exception.Core(fmt.Sprintf("%s字段 %s 重复", label, reference.name))
		}
		target[field.Name()] = struct{}{}
	}

	return nil
}

func compileSortFields(
	metadata gnentity.Metadata,
	entityType reflect.Type,
	input FieldPolicyInput,
	policy *FieldPolicy,
	autoSortFields bool,
) error {
	root := resolvedPlanEntity{
		planEntity: planEntity{
			table:      metadata.Table(),
			alias:      rootQueryAlias,
			entityType: entityType,
		},
		metadata: metadata,
	}
	for _, reference := range input.SortFields {
		field, err := resolvePolicyField(metadata, entityType, reference, "排序")
		if err != nil {
			return err
		}
		if _, exists := policy.sortable[field.JSONName()]; exists {
			return exception.Core(fmt.Sprintf("排序字段 %s 重复", reference.name))
		}
		policy.sortable[field.JSONName()] = makePlanColumn(root, field)
	}
	if autoSortFields {
		for _, field := range metadata.PersistentFields() {
			if policy.IsHidden(field.Name()) {
				continue
			}
			policy.sortable[field.JSONName()] = makePlanColumn(root, field)
		}
	}

	hasDefaultSort := hasColumnRef(input.DefaultSort)
	if !hasDefaultSort {
		if input.DefaultOrder != "" {
			return exception.Core("默认排序方向必须与默认排序字段同时配置")
		}
		if autoSortFields {
			policy.defaultSort = &planOrder{
				column:    makePlanColumn(root, metadata.Primary()),
				direction: Descending,
			}
		}

		return nil
	}
	if input.DefaultOrder != Ascending && input.DefaultOrder != Descending {
		return exception.Core("默认排序方向无效")
	}
	field, err := resolvePolicyField(metadata, entityType, input.DefaultSort, "默认排序")
	if err != nil {
		return err
	}
	column, exists := policy.sortable[field.JSONName()]
	if !exists {
		return exception.Core(fmt.Sprintf("默认排序字段 %s 不在排序白名单中", field.JSONName()))
	}
	policy.defaultSort = &planOrder{column: column, direction: input.DefaultOrder}

	return nil
}

func resolvePolicyField(
	metadata gnentity.Metadata,
	entityType reflect.Type,
	reference ColumnRef,
	label string,
) (gnentity.Field, error) {
	if !hasColumnRef(reference) || (reference.alias != "" && reference.alias != rootQueryAlias) {
		return nil, exception.Core(fmt.Sprintf("%s字段引用无效", label))
	}
	if reference.entityType != nil && reference.entityType != entityType {
		return nil, exception.Core(fmt.Sprintf("%s字段 %s 的实体类型不匹配", label, reference.name))
	}
	field, exists := metadata.Field(reference.name)
	if !exists || isNilPlanValue(field) {
		return nil, exception.Core(fmt.Sprintf("%s字段 %s 不存在", label, reference.name))
	}
	if !field.Persistent() {
		return nil, exception.Core(fmt.Sprintf("%s字段 %s 不是持久化字段", label, reference.name))
	}

	return field, nil
}

func applyFieldPolicy(
	action Action,
	query *QueryPlan,
	policy *FieldPolicy,
	request *QueryRequest,
) error {
	selects := query.selects[:0]
	for _, selected := range query.selects {
		isRoot := selected.column.alias == rootQueryAlias
		if isRoot && policy.IsHidden(selected.column.name) {
			continue
		}
		if action == ActionInfo && isRoot && policy.IsInfoIgnored(selected.column.name) {
			continue
		}
		selects = append(selects, selected)
	}
	query.selects = selects
	if len(query.selects) == 0 {
		return exception.Core("字段策略移除了全部查询字段")
	}
	if action != ActionList && action != ActionPage {
		return nil
	}

	return applyOrder(query, policy, request)
}

func applyOrder(query *QueryPlan, policy *FieldPolicy, request *QueryRequest) error {
	orderFields, hasOrder, err := requestStrings(request, "order")
	if err != nil {
		return err
	}
	directions, hasSort, err := requestStrings(request, "sort")
	if err != nil {
		return err
	}
	if hasOrder != hasSort || (hasOrder && len(orderFields) != len(directions)) {
		return exception.Validate("order 与 sort 数量必须一致")
	}
	if !hasOrder {
		if policy.defaultSort != nil {
			query.orderBy = append(query.orderBy, *policy.defaultSort)
		}

		return nil
	}
	if len(orderFields) == 0 || len(query.orderBy) > maxQueryPlanNodes-len(orderFields) {
		return exception.Validate("请求排序字段数量无效")
	}
	for index, name := range orderFields {
		column, exists := policy.sortable[name]
		if !exists {
			return exception.Validate(fmt.Sprintf("排序字段 %s 不在白名单中", name))
		}
		direction := Direction(directions[index])
		if direction != Ascending && direction != Descending {
			return exception.Validate(fmt.Sprintf("排序方向 %s 无效", directions[index]))
		}
		query.orderBy = append(query.orderBy, planOrder{column: column, direction: direction})
	}

	return nil
}

func requestStrings(request *QueryRequest, name string) ([]string, bool, error) {
	if request == nil {
		return nil, false, nil
	}
	value, exists := request.Value(name)
	if !exists {
		return nil, false, nil
	}
	values, matches := value.([]string)
	if !matches {
		return nil, false, exception.Validate(fmt.Sprintf("查询请求字段 %s 必须为字符串数组", name))
	}

	return values, true, nil
}

func emptyPolicy(input FieldPolicyInput) bool {
	return len(input.HiddenFields) == 0 &&
		len(input.ReadonlyFields) == 0 &&
		len(input.InfoIgnoreProperty) == 0 &&
		len(input.SortFields) == 0 &&
		!hasColumnRef(input.DefaultSort) &&
		input.DefaultOrder == ""
}

func hasColumnRef(reference ColumnRef) bool {
	return reference.name != "" || reference.entityType != nil || reference.alias != ""
}
