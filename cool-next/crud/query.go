package crud

import "context"

// 查询配置
type QueryOp struct {
	KeyWordLikeFields []ColumnRef   // 关键词模糊匹配字段
	Where             WhereProvider // 固定查询条件
	Select            []SelectField // 查询字段
	FieldLike         []FieldLike   // 请求模糊匹配
	FieldEq           []FieldEq     // 请求等值匹配
	AddOrderBy        []Order       // 固定排序
	Join              []JoinOp      // 关联查询
	Extend            QueryExtender // 追加查询扩展
	shape             queryShape    // Controller 响应形状策略
}

type queryShape uint8

const (
	queryShapeStatic queryShape = iota + 1
	queryShapeDynamic
)

// 请求字段匹配
type FieldMatch struct {
	Column       ColumnRef // 匹配字段
	RequestParam string    // 请求参数名
}

// 请求等值匹配
type FieldEq = FieldMatch

// 请求模糊匹配
type FieldLike = FieldMatch

// 查询追加函数
type QueryExtender func(context.Context, *QueryBuilder, *QueryRequest) error

// 追加式查询构造器
type QueryBuilder struct {
	where   []Condition
	selects []SelectField
	joins   []JoinOp
	groupBy []ColumnRef
	having  []Condition
	orderBy []Order
}

// 标记静态查询响应形状
func StaticShape(op QueryOp) QueryOp {
	op.shape = queryShapeStatic

	return op
}

// 标记动态查询响应形状
func DynamicShape(op QueryOp) QueryOp {
	op.shape = queryShapeDynamic

	return op
}

// 构造默认等值匹配
func Eq(column ColumnRef) FieldEq {
	return newFieldMatch(column, column.name)
}

// 构造指定请求参数的等值匹配
func EqFrom(column ColumnRef, requestParam string) FieldEq {
	return newFieldMatch(column, requestParam)
}

// 构造默认模糊匹配
func Like(column ColumnRef) FieldLike {
	return newFieldMatch(column, column.name)
}

// 构造指定请求参数的模糊匹配
func LikeFrom(column ColumnRef, requestParam string) FieldLike {
	return newFieldMatch(column, requestParam)
}

// 保留查询追加函数
func Extend(extender QueryExtender) QueryExtender {
	if extender == nil {
		panicCore("查询扩展函数不能为空")
	}

	return extender
}

// 追加查询条件
func (query *QueryBuilder) Where(conditions ...Condition) *QueryBuilder {
	query.require()
	requireConditions(conditions)
	query.where = append(query.where, conditions...)

	return query
}

// 追加大于条件
func (query *QueryBuilder) WhereGT(column ColumnRef, value any) *QueryBuilder {
	return query.Where(compare(operatorGT, column, value))
}

// 追加大于等于条件
func (query *QueryBuilder) WhereGTE(column ColumnRef, value any) *QueryBuilder {
	return query.Where(compare(operatorGTE, column, value))
}

// 追加小于条件
func (query *QueryBuilder) WhereLT(column ColumnRef, value any) *QueryBuilder {
	return query.Where(compare(operatorLT, column, value))
}

// 追加小于等于条件
func (query *QueryBuilder) WhereLTE(column ColumnRef, value any) *QueryBuilder {
	return query.Where(compare(operatorLTE, column, value))
}

// 追加查询字段
func (query *QueryBuilder) AddSelect(fields ...SelectField) *QueryBuilder {
	query.require()
	for _, field := range fields {
		if field == nil {
			panicCore("查询字段不能为空")
		}
	}
	query.selects = append(query.selects, fields...)

	return query
}

// 追加关联查询
func (query *QueryBuilder) AddJoin(joins ...JoinOp) *QueryBuilder {
	query.require()
	for _, join := range joins {
		checkJoin(join)
	}
	query.joins = append(query.joins, joins...)

	return query
}

// 追加分组字段
func (query *QueryBuilder) AddGroupBy(fields ...ColumnRef) *QueryBuilder {
	query.require()
	for _, field := range fields {
		checkColumn(field)
	}
	query.groupBy = append(query.groupBy, fields...)

	return query
}

// 追加 Having 条件
func (query *QueryBuilder) AddHaving(conditions ...Condition) *QueryBuilder {
	query.require()
	requireConditions(conditions)
	query.having = append(query.having, conditions...)

	return query
}

// 追加排序
func (query *QueryBuilder) AddOrderBy(orders ...Order) *QueryBuilder {
	query.require()
	for _, order := range orders {
		checkOrder(order)
	}
	query.orderBy = append(query.orderBy, orders...)

	return query
}

// 构造请求字段匹配
func newFieldMatch(column ColumnRef, requestParam string) FieldMatch {
	checkColumn(column)
	if !queryNamePattern.MatchString(requestParam) {
		panicCore("请求参数名 %q 无效", requestParam)
	}

	return FieldMatch{Column: column, RequestParam: requestParam}
}

// 查询构造器
func (query *QueryBuilder) require() {
	if query == nil {
		panicCore("查询构造器不能为空")
	}
}

// 查询条件
func requireConditions(conditions []Condition) {
	for _, condition := range conditions {
		if condition == nil {
			panicCore("查询条件不能为空")
		}
	}
}
