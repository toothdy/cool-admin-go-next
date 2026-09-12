package crud

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

const (
	maxQueryPlanJoins = 8
	maxQueryPlanNodes = 128
	maxPlanBindings   = 1000
	rootQueryAlias    = "a"
)

const operatorKeyword conditionOperator = "keyword"

// Descriptor 解析器
type DescriptorResolver interface {
	Resolve(entity any) (gnentity.Metadata, bool)
}

// 不可变查询计划
type QueryPlan struct {
	root    planEntity
	joins   []planJoin
	selects []planSelect
	where   []planCondition
	groupBy []planColumn
	having  []planCondition
	orderBy []planOrder
}

type planEntity struct {
	table      string
	alias      string
	entityType reflect.Type
}

type resolvedPlanEntity struct {
	planEntity
	metadata gnentity.Metadata
}

type planColumn struct {
	table       string
	alias       string
	column      string
	name        string
	jsonName    string
	goType      reflect.Type
	logicalType gnentity.LogicalType
	nullable    bool
}

type planJoin struct {
	joinType JoinType
	table    string
	alias    string
	left     planColumn
	right    planColumn
}

type planSelect struct {
	column planColumn
	alias  string
}

type planCondition struct {
	operator   conditionOperator
	column     planColumn
	columns    []planColumn
	value      any
	expression string
	arguments  []any
}

type planOrder struct {
	column    planColumn
	direction Direction
}

type queryPlanCompiler struct {
	resolver     DescriptorResolver
	aliases      map[string]resolvedPlanEntity
	plan         QueryPlan
	nodeCount    int
	bindingCount int
}

func compileQueryPlan(
	ctx context.Context,
	resolver DescriptorResolver,
	rootEntity any,
	op QueryOp,
	request *QueryRequest,
) (*QueryPlan, error) {
	if isNilPlanValue(ctx) {
		return nil, exception.Core("查询上下文不能为空")
	}
	compiler, err := newPlanCompiler(resolver, rootEntity)
	if err != nil {
		return nil, err
	}

	builder := &QueryBuilder{}
	if op.Extend != nil {
		if err = op.Extend(ctx, builder, request); err != nil {
			var coolError *exception.BaseException
			if errors.As(err, &coolError) {
				return nil, err
			}

			return nil, exception.WrapCore(err, "查询扩展执行失败")
		}
	}

	joins := append(append([]JoinOp(nil), op.Join...), builder.joins...)
	if len(joins) > maxQueryPlanJoins {
		return nil, exception.Core("关联查询数量超过上限")
	}
	for _, join := range joins {
		if err = compiler.compileJoin(join); err != nil {
			return nil, err
		}
	}

	selects, err := mergeSelects(op, builder.selects)
	if err != nil {
		return nil, err
	}
	if len(selects) == 0 {
		selects = []SelectField{allSelectNode{alias: rootQueryAlias}}
	}
	if err = compiler.compileSelects(selects); err != nil {
		return nil, err
	}
	if err = compiler.compileWhere(op.Where); err != nil {
		return nil, err
	}
	if err = compiler.compileKeyword(op.KeyWordLikeFields, request); err != nil {
		return nil, err
	}
	if err = compiler.compileFieldLikes(op.FieldLike, request); err != nil {
		return nil, err
	}
	if err = compiler.compileFieldEqs(op.FieldEq, request); err != nil {
		return nil, err
	}
	if err = compiler.compileConditions(builder.where, &compiler.plan.where); err != nil {
		return nil, err
	}
	if err = compiler.compileGroups(builder.groupBy); err != nil {
		return nil, err
	}
	if err = compiler.compileConditions(builder.having, &compiler.plan.having); err != nil {
		return nil, err
	}
	if err = compiler.compileOrders(append(append([]Order(nil), op.AddOrderBy...), builder.orderBy...)); err != nil {
		return nil, err
	}

	return &compiler.plan, nil
}

func newPlanCompiler(resolver DescriptorResolver, rootEntity any) (*queryPlanCompiler, error) {
	if isNilPlanValue(resolver) {
		return nil, exception.Core("Descriptor 解析器不能为空")
	}
	if isNilPlanValue(rootEntity) {
		return nil, exception.Core("根实体不能为空")
	}

	compiler := &queryPlanCompiler{
		resolver: resolver,
		aliases:  make(map[string]resolvedPlanEntity),
	}
	root, err := compiler.resolveEntity(rootEntity, rootQueryAlias)
	if err != nil {
		return nil, err
	}
	compiler.plan.root = root.planEntity
	compiler.aliases[rootQueryAlias] = root

	return compiler, nil
}

func mergeSelects(op QueryOp, dynamic []SelectField) ([]SelectField, error) {
	selects := append([]SelectField(nil), op.Select...)
	switch op.shape {
	case queryShapeStatic:
		return mergeStaticSelects(selects, dynamic)
	case queryShapeDynamic:
		if len(dynamic) > 0 {
			return nil, exception.Core("动态查询扩展不能追加响应字段")
		}
		if len(selects) == 0 {
			return selects, nil
		}
		if len(selects) == 1 {
			all, matches := selects[0].(allSelectNode)
			if matches && all.alias == rootQueryAlias {
				return selects, nil
			}
		}

		return nil, exception.Core("动态查询不能改变默认响应形状")
	default:
		return append(selects, dynamic...), nil
	}
}

func mergeStaticSelects(static, dynamic []SelectField) ([]SelectField, error) {
	if len(dynamic) == 0 {
		return static, nil
	}
	indexes := make(map[string]int, len(static))
	for index, selected := range static {
		node, matches := selected.(aliasedSelectNode)
		if !matches {
			continue
		}
		if _, exists := indexes[node.alias]; exists {
			indexes[node.alias] = -1
			continue
		}
		indexes[node.alias] = index
	}
	replaced := make(map[string]struct{}, len(dynamic))
	for _, selected := range dynamic {
		node, matches := selected.(aliasedSelectNode)
		if !matches {
			return nil, exception.Core("静态查询扩展不能动态展开响应字段")
		}
		index, exists := indexes[node.alias]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("静态查询扩展输出别名 %s 未声明", node.alias))
		}
		if index < 0 {
			return nil, exception.Core(fmt.Sprintf("静态查询输出别名 %s 重复", node.alias))
		}
		if _, exists = replaced[node.alias]; exists {
			return nil, exception.Core(fmt.Sprintf("静态查询扩展输出别名 %s 重复", node.alias))
		}
		replaced[node.alias] = struct{}{}
		static[index] = selected
	}

	return static, nil
}

func (compiler *queryPlanCompiler) resolveEntity(value any, alias string) (resolvedPlanEntity, error) {
	entityType := reflect.TypeOf(value)
	if entityType == nil || entityType.Kind() != reflect.Struct || entityType.Name() == "" || entityType.PkgPath() == "" {
		return resolvedPlanEntity{}, exception.Core("实体类型无效")
	}
	metadata, exists := compiler.resolver.Resolve(value)
	if !exists || isNilPlanValue(metadata) {
		return resolvedPlanEntity{}, exception.Core(fmt.Sprintf("实体 %s 的 Descriptor 不存在", entityType))
	}
	if isNilPlanValue(metadata.Primary()) {
		return resolvedPlanEntity{}, exception.Core(fmt.Sprintf("实体 %s 的 Descriptor 缺少主键", entityType))
	}

	return resolvedPlanEntity{
		planEntity: planEntity{
			table:      metadata.Table(),
			alias:      alias,
			entityType: entityType,
		},
		metadata: metadata,
	}, nil
}

func (compiler *queryPlanCompiler) compileJoin(join JoinOp) error {
	if !queryNamePattern.MatchString(join.Alias) {
		return exception.Core("关联别名无效")
	}
	if join.Type != JoinLeft && join.Type != JoinInner {
		return exception.Core(fmt.Sprintf("关联 %s 的类型无效", join.Alias))
	}
	if join.Alias == rootQueryAlias {
		return exception.Core(fmt.Sprintf("关联别名 %s 为保留别名", join.Alias))
	}
	if _, exists := compiler.aliases[join.Alias]; exists {
		return exception.Core(fmt.Sprintf("关联别名 %s 重复", join.Alias))
	}
	current, err := compiler.resolveEntity(join.Entity, join.Alias)
	if err != nil {
		return err
	}
	condition, matches := join.Condition.(conditionNode)
	if !matches || condition.operator != operatorOn {
		return exception.Core(fmt.Sprintf("关联 %s 的条件无效", join.Alias))
	}

	compiler.aliases[join.Alias] = current
	left, leftErr := compiler.resolveColumn(condition.column)
	right, rightErr := compiler.resolveColumn(condition.right)
	if leftErr != nil || rightErr != nil {
		delete(compiler.aliases, join.Alias)
		if leftErr != nil {
			return leftErr
		}

		return rightErr
	}
	leftIsCurrent := left.alias == join.Alias
	rightIsCurrent := right.alias == join.Alias
	if leftIsCurrent == rightIsCurrent {
		delete(compiler.aliases, join.Alias)
		return exception.Core(fmt.Sprintf("关联 %s 的字段关系不可达", join.Alias))
	}
	if planValueType(left.goType) != planValueType(right.goType) {
		delete(compiler.aliases, join.Alias)
		return exception.Core(fmt.Sprintf("关联 %s 的字段类型不一致", join.Alias))
	}

	compiler.plan.joins = append(compiler.plan.joins, planJoin{
		joinType: join.Type,
		table:    current.table,
		alias:    join.Alias,
		left:     left,
		right:    right,
	})

	return nil
}

func (compiler *queryPlanCompiler) compileSelects(selects []SelectField) error {
	outputs := make(map[string]struct{})
	for _, selected := range selects {
		switch node := selected.(type) {
		case allSelectNode:
			resolved, exists := compiler.aliases[node.alias]
			if !exists {
				return exception.Core(fmt.Sprintf("查询别名 %s 不存在", node.alias))
			}
			for _, field := range resolved.metadata.PersistentFields() {
				if isNilPlanValue(field) {
					return exception.Core(fmt.Sprintf("查询别名 %s 包含无效字段", node.alias))
				}
				column := makePlanColumn(resolved, field)
				if err := compiler.appendSelect(column, field.JSONName(), outputs); err != nil {
					return err
				}
			}
		case aliasedSelectNode:
			column, err := compiler.resolveColumn(node.column)
			if err != nil {
				return err
			}
			if err = compiler.appendSelect(column, node.alias, outputs); err != nil {
				return err
			}
		default:
			return exception.Core("查询字段节点无效")
		}
	}

	return nil
}

func (compiler *queryPlanCompiler) appendSelect(column planColumn, alias string, outputs map[string]struct{}) error {
	if _, exists := outputs[alias]; exists {
		return exception.Core(fmt.Sprintf("查询输出名 %s 重复", alias))
	}
	if err := compiler.addNodes(1); err != nil {
		return err
	}
	outputs[alias] = struct{}{}
	compiler.plan.selects = append(compiler.plan.selects, planSelect{column: column, alias: alias})

	return nil
}

func (compiler *queryPlanCompiler) compileWhere(provider WhereProvider) error {
	if provider == nil {
		return nil
	}
	node, matches := provider.(whereNode)
	if !matches {
		return exception.Core("固定查询条件无效")
	}

	return compiler.compileConditions(node.conditions, &compiler.plan.where)
}

func (compiler *queryPlanCompiler) compileConditions(conditions []Condition, destination *[]planCondition) error {
	for _, condition := range append([]Condition(nil), conditions...) {
		node, err := compiler.compileCondition(condition)
		if err != nil {
			return err
		}
		*destination = append(*destination, node)
	}

	return nil
}

func (compiler *queryPlanCompiler) compileCondition(condition Condition) (planCondition, error) {
	node, matches := condition.(conditionNode)
	if !matches {
		return planCondition{}, exception.Core("查询条件节点无效")
	}
	if node.operator == operatorRaw {
		if err := compiler.addNodes(1); err != nil {
			return planCondition{}, err
		}
		if err := compiler.addBindings(len(node.arguments), false); err != nil {
			return planCondition{}, err
		}

		return planCondition{
			operator:   operatorRaw,
			expression: node.expression,
			arguments:  cloneArgs(node.arguments),
		}, nil
	}
	if node.operator == operatorOn || node.operator == operatorKeyword {
		return planCondition{}, exception.Core("查询条件操作符无效")
	}
	column, err := compiler.resolveColumn(node.column)
	if err != nil {
		return planCondition{}, err
	}
	if err = compiler.addNodes(1); err != nil {
		return planCondition{}, err
	}

	result := planCondition{operator: node.operator, column: column}
	switch node.operator {
	case operatorEQ, operatorNE:
		if isNilPlanValue(node.value) {
			if !column.nullable {
				return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 不可为空", column.name))
			}
			return result, nil
		}
		if !validPlanValue(node.value, column.goType) {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 的查询值类型不匹配", column.name))
		}
		if err = compiler.addBindings(1, false); err != nil {
			return planCondition{}, err
		}
		result.value = clonePlanValue(node.value)
	case operatorIn:
		values, valueErr := planValues(node.value, column.goType)
		if valueErr != nil {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 的集合查询值无效", column.name))
		}
		if err = compiler.addBindings(len(values), false); err != nil {
			return planCondition{}, err
		}
		result.value = values
	case operatorLike:
		if column.logicalType != gnentity.LogicalString {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 不支持模糊查询", column.name))
		}
		value, matches := node.value.(string)
		if !matches {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 的模糊查询值类型不匹配", column.name))
		}
		if err = compiler.addBindings(1, false); err != nil {
			return planCondition{}, err
		}
		result.value = value
	case operatorGT, operatorGTE, operatorLT, operatorLTE:
		if !supportsOrdering(column.logicalType) {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 不支持范围比较", column.name))
		}
		if isNilPlanValue(node.value) || !validPlanValue(node.value, column.goType) {
			return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 的范围查询值类型不匹配", column.name))
		}
		if err = compiler.addBindings(1, false); err != nil {
			return planCondition{}, err
		}
		result.value = clonePlanValue(node.value)
	default:
		return planCondition{}, exception.Core(fmt.Sprintf("字段 %s 的查询操作符无效", column.name))
	}

	return result, nil
}

func (compiler *queryPlanCompiler) compileKeyword(fields []ColumnRef, request *QueryRequest) error {
	columns := make([]planColumn, 0, len(fields))
	for _, field := range append([]ColumnRef(nil), fields...) {
		column, err := compiler.resolveColumn(field)
		if err != nil {
			return err
		}
		if column.logicalType != gnentity.LogicalString {
			return exception.Core(fmt.Sprintf("字段 %s 不支持关键词查询", column.name))
		}
		columns = append(columns, column)
	}
	value, exists := request.Value("keyWord")
	if !exists {
		return nil
	}
	keyword, matches := value.(string)
	if value == nil || !matches {
		return exception.Validate("请求参数 keyWord 的类型无效")
	}
	if keyword == "" || len(columns) == 0 {
		return nil
	}
	if err := compiler.addNodes(len(columns)); err != nil {
		return err
	}
	if err := compiler.addBindings(len(columns), true); err != nil {
		return err
	}
	pattern := "%" + keyword + "%"
	arguments := make([]any, len(columns))
	for index := range arguments {
		arguments[index] = pattern
	}
	compiler.plan.where = append(compiler.plan.where, planCondition{
		operator:  operatorKeyword,
		columns:   append([]planColumn(nil), columns...),
		arguments: arguments,
	})

	return nil
}

func (compiler *queryPlanCompiler) compileFieldLikes(matches []FieldLike, request *QueryRequest) error {
	for _, match := range append([]FieldLike(nil), matches...) {
		if !queryNamePattern.MatchString(match.RequestParam) {
			return exception.Core("模糊查询请求参数名无效")
		}
		column, err := compiler.resolveColumn(match.Column)
		if err != nil {
			return err
		}
		if column.logicalType != gnentity.LogicalString {
			return exception.Core(fmt.Sprintf("字段 %s 不支持模糊查询", column.name))
		}
		value, exists := request.Value(match.RequestParam)
		if !exists {
			continue
		}
		text, valid := value.(string)
		if value == nil || !valid {
			return exception.Validate(fmt.Sprintf("请求参数 %s 的类型无效", match.RequestParam))
		}
		if err = compiler.addNodes(1); err != nil {
			return err
		}
		if err = compiler.addBindings(1, true); err != nil {
			return err
		}
		compiler.plan.where = append(compiler.plan.where, planCondition{
			operator: operatorLike,
			column:   column,
			value:    "%" + text + "%",
		})
	}

	return nil
}

func (compiler *queryPlanCompiler) compileFieldEqs(matches []FieldEq, request *QueryRequest) error {
	for _, match := range append([]FieldEq(nil), matches...) {
		if !queryNamePattern.MatchString(match.RequestParam) {
			return exception.Core("等值查询请求参数名无效")
		}
		column, err := compiler.resolveColumn(match.Column)
		if err != nil {
			return err
		}
		value, exists := request.Value(match.RequestParam)
		if !exists {
			continue
		}
		condition := planCondition{operator: operatorEQ, column: column}
		if value == nil {
			if !column.nullable {
				return exception.Validate(fmt.Sprintf("请求参数 %s 不可为空", match.RequestParam))
			}
		} else {
			reflected := reflect.ValueOf(value)
			if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
				if reflected.Len() == 0 {
					continue
				}
				values, valueErr := planValues(value, column.goType)
				if valueErr != nil {
					return exception.Validate(fmt.Sprintf("请求参数 %s 的集合值无效", match.RequestParam))
				}
				if err = compiler.addBindings(len(values), true); err != nil {
					return err
				}
				condition.operator = operatorIn
				condition.value = values
			} else {
				normalized, valid := planValue(value, column.goType)
				if !valid {
					return exception.Validate(fmt.Sprintf("请求参数 %s 的类型无效", match.RequestParam))
				}
				if err = compiler.addBindings(1, true); err != nil {
					return err
				}
				condition.value = normalized
			}
		}
		if err = compiler.addNodes(1); err != nil {
			return err
		}
		compiler.plan.where = append(compiler.plan.where, condition)
	}

	return nil
}

func (compiler *queryPlanCompiler) compileGroups(groups []ColumnRef) error {
	for _, group := range append([]ColumnRef(nil), groups...) {
		column, err := compiler.resolveColumn(group)
		if err != nil {
			return err
		}
		if err = compiler.addNodes(1); err != nil {
			return err
		}
		compiler.plan.groupBy = append(compiler.plan.groupBy, column)
	}

	return nil
}

func (compiler *queryPlanCompiler) compileOrders(orders []Order) error {
	for _, order := range orders {
		if order.Direction != Ascending && order.Direction != Descending {
			return exception.Core("查询排序方向无效")
		}
		column, err := compiler.resolveColumn(order.Column)
		if err != nil {
			return err
		}
		if err = compiler.addNodes(1); err != nil {
			return err
		}
		compiler.plan.orderBy = append(compiler.plan.orderBy, planOrder{column: column, direction: order.Direction})
	}

	return nil
}

func (compiler *queryPlanCompiler) resolveColumn(reference ColumnRef) (planColumn, error) {
	alias := reference.alias
	if alias == "" {
		alias = rootQueryAlias
	}
	resolved, exists := compiler.aliases[alias]
	if !exists {
		return planColumn{}, exception.Core(fmt.Sprintf("查询别名 %s 不存在", alias))
	}
	if reference.entityType != nil && reference.entityType != resolved.entityType {
		return planColumn{}, exception.Core(fmt.Sprintf("字段 %s 的实体类型与别名 %s 不一致", reference.name, alias))
	}
	field, exists := resolved.metadata.Field(reference.name)
	if !exists || isNilPlanValue(field) {
		return planColumn{}, exception.Core(fmt.Sprintf("实体别名 %s 的字段 %s 不存在", alias, reference.name))
	}
	if !field.Persistent() {
		return planColumn{}, exception.Core(fmt.Sprintf("实体别名 %s 的字段 %s 不是持久化字段", alias, reference.name))
	}

	return makePlanColumn(resolved, field), nil
}

func (compiler *queryPlanCompiler) addNodes(count int) error {
	if count < 0 || compiler.nodeCount > maxQueryPlanNodes-count {
		return exception.Core("查询节点数量超过上限")
	}
	compiler.nodeCount += count

	return nil
}

func (compiler *queryPlanCompiler) addBindings(count int, fromRequest bool) error {
	if count >= 0 && compiler.bindingCount <= maxPlanBindings-count {
		compiler.bindingCount += count
		return nil
	}
	if fromRequest {
		return exception.Validate("查询请求绑定值数量超过上限")
	}

	return exception.Core("查询绑定值数量超过上限")
}

func makePlanColumn(resolved resolvedPlanEntity, field gnentity.Field) planColumn {
	return planColumn{
		table:       resolved.table,
		alias:       resolved.alias,
		column:      field.Column(),
		name:        field.Name(),
		jsonName:    field.JSONName(),
		goType:      field.GoType(),
		logicalType: field.LogicalType(),
		nullable:    field.Nullable(),
	}
}

func planValues(value any, target reflect.Type) ([]any, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || (reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array) || reflected.Len() == 0 {
		return nil, errors.New("集合不能为空")
	}
	values := make([]any, reflected.Len())
	for index := 0; index < reflected.Len(); index++ {
		item := reflected.Index(index).Interface()
		normalized, valid := planValue(item, target)
		if !valid {
			return nil, errors.New("集合元素类型无效")
		}
		values[index] = normalized
	}

	return values, nil
}

func planValue(value any, target reflect.Type) (any, bool) {
	if isNilPlanValue(value) || target == nil {
		return nil, false
	}
	target = planValueType(target)
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(target) {
		return clonePlanValue(value), true
	}
	result := reflect.New(target).Elem()
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number, valid := requestInt64(source)
		if !valid || result.OverflowInt(number) {
			return nil, false
		}
		result.SetInt(number)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		number, valid := requestUint64(source)
		if !valid || result.OverflowUint(number) {
			return nil, false
		}
		result.SetUint(number)
	case reflect.Float32, reflect.Float64:
		number, valid := requestFloat64(source)
		if !valid || result.OverflowFloat(number) {
			return nil, false
		}
		result.SetFloat(number)
	default:
		return nil, false
	}

	return result.Interface(), true
}

func requestInt64(value reflect.Value) (int64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if value.Uint() > math.MaxInt64 {
			return 0, false
		}
		return int64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < math.MinInt64 || number >= -float64(math.MinInt64) {
			return 0, false
		}
		return int64(number), true
	default:
		return 0, false
	}
}

func requestUint64(value reflect.Value) (uint64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Int() < 0 {
			return 0, false
		}
		return uint64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint(), true
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number != math.Trunc(number) || number >= math.Exp2(64) {
			return 0, false
		}
		return uint64(number), true
	default:
		return 0, false
	}
}

func requestFloat64(value reflect.Value) (float64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return 0, false
	}
}

func validPlanValue(value any, target reflect.Type) bool {
	if isNilPlanValue(value) || target == nil {
		return false
	}

	return reflect.TypeOf(value).AssignableTo(planValueType(target))
}

func planValueType(valueType reflect.Type) reflect.Type {
	if valueType != nil && valueType.Kind() == reflect.Pointer {
		return valueType.Elem()
	}

	return valueType
}

func supportsOrdering(logicalType gnentity.LogicalType) bool {
	switch logicalType {
	case gnentity.LogicalInt, gnentity.LogicalUint, gnentity.LogicalFloat, gnentity.LogicalString, gnentity.LogicalTime:
		return true
	default:
		return false
	}
}

func cloneArgs(arguments []any) []any {
	cloned := make([]any, len(arguments))
	for index, argument := range arguments {
		cloned[index] = clonePlanValue(argument)
	}

	return cloned
}

func clonePlanValue(value any) any {
	if value == nil {
		return nil
	}

	return cloneReflect(reflect.ValueOf(value)).Interface()
}

func cloneReflect(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneReflect(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflect(value.Index(index)))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflect(value.Index(index)))
		}
		return result
	default:
		return value
	}
}

func isNilPlanValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
