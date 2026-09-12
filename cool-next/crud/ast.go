package crud

import (
	"reflect"
	"strings"
)

// 排序方向
type Direction string

const (
	Ascending  Direction = "asc"  // 升序
	Descending Direction = "desc" // 降序
)

// 逻辑字段引用
type ColumnRef struct {
	name       string
	entityType reflect.Type
	alias      string
}

// 构造根实体字段引用
func NewColumnRef(name string) ColumnRef {
	checkName("字段名", name)

	return ColumnRef{name: name}
}

// 构造指定实体字段引用
func NewColumnRefOf[E any](name string) ColumnRef {
	column := NewColumnRef(name)
	column.entityType = reflect.TypeFor[E]()
	checkEntityType(column.entityType)

	return column
}

// 绑定字段所属别名
func (column ColumnRef) Of(alias string) ColumnRef {
	checkColumn(column)
	checkName("字段别名", alias)
	column.alias = alias

	return column
}

// 结构化查询条件
type Condition interface {
	condition()
}

// 条件组合入口
type WhereProvider interface {
	whereProvider()
}

type conditionOperator string

const (
	operatorEQ   conditionOperator = "eq"
	operatorNE   conditionOperator = "ne"
	operatorGT   conditionOperator = "gt"
	operatorGTE  conditionOperator = "gte"
	operatorLT   conditionOperator = "lt"
	operatorLTE  conditionOperator = "lte"
	operatorIn   conditionOperator = "in"
	operatorLike conditionOperator = "like"
	operatorRaw  conditionOperator = "raw"
	operatorOn   conditionOperator = "on"
)

type conditionNode struct {
	operator   conditionOperator
	column     ColumnRef
	right      ColumnRef
	value      any
	expression string
	arguments  []any
}

type whereNode struct {
	conditions []Condition
}

func (conditionNode) condition() {}

func (whereNode) whereProvider() {}

// 组合 AND 条件
func Where(conditions ...Condition) WhereProvider {
	if len(conditions) == 0 {
		panicCore("Where 至少需要一个条件")
	}
	for _, condition := range conditions {
		if condition == nil {
			panicCore("Where 条件不能为空")
		}
	}

	return whereNode{conditions: append([]Condition(nil), conditions...)}
}

// 构造等值条件
func EqValue(column ColumnRef, value any) Condition {
	return compare(operatorEQ, column, value)
}

// 构造不等条件
func NeValue(column ColumnRef, value any) Condition {
	return compare(operatorNE, column, value)
}

// 构造集合条件
func In(column ColumnRef, values any) Condition {
	checkColumn(column)
	reflected := reflect.ValueOf(values)
	if !reflected.IsValid() || (reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array) || reflected.Len() == 0 {
		panicCore("In 只接受非空 slice 或 array")
	}

	return conditionNode{operator: operatorIn, column: column, value: cloneRequestData(values)}
}

// 构造模糊匹配条件
func LikeValue(column ColumnRef, value string) Condition {
	checkColumn(column)

	return conditionNode{operator: operatorLike, column: column, value: value}
}

// 构造原始参数化条件
func RawWhere(expression string, args ...any) Condition {
	if strings.TrimSpace(expression) == "" {
		panicCore("RawWhere 表达式不能为空")
	}
	arguments := make([]any, len(args))
	for index, argument := range args {
		arguments[index] = cloneRequestData(argument)
	}

	return conditionNode{operator: operatorRaw, expression: expression, arguments: arguments}
}

// 构造字段关联条件
func On(left ColumnRef, right ColumnRef) Condition {
	checkColumn(left)
	checkColumn(right)

	return conditionNode{operator: operatorOn, column: left, right: right}
}

// 可选字段节点
type SelectField interface {
	selectField()
}

type allSelectNode struct {
	alias string
}

type aliasedSelectNode struct {
	column ColumnRef
	alias  string
}

func (allSelectNode) selectField() {}

func (aliasedSelectNode) selectField() {}

// 选择别名实体全部字段
func All(alias string) SelectField {
	checkName("实体别名", alias)

	return allSelectNode{alias: alias}
}

// 选择字段并指定输出别名
func As(column ColumnRef, alias string) SelectField {
	checkColumn(column)
	checkName("输出别名", alias)

	return aliasedSelectNode{column: column, alias: alias}
}

// 关联类型
type JoinType string

const (
	JoinLeft  JoinType = "left"  // 左关联
	JoinInner JoinType = "inner" // 内关联
)

// 关联节点
type JoinOp struct {
	Entity    any       // 关联实体
	Alias     string    // 实体别名
	Condition Condition // 关联条件
	Type      JoinType  // 关联类型
}

// 构造左关联
func LeftJoin(entity any, alias string, on Condition) JoinOp {
	return newJoin(JoinLeft, entity, alias, on)
}

// 构造内关联
func InnerJoin(entity any, alias string, on Condition) JoinOp {
	return newJoin(JoinInner, entity, alias, on)
}

// 排序节点
type Order struct {
	Column    ColumnRef // 排序字段
	Direction Direction // 排序方向
}

// 构造升序节点
func Asc(column ColumnRef) Order {
	return newOrder(column, Ascending)
}

// 构造降序节点
func Desc(column ColumnRef) Order {
	return newOrder(column, Descending)
}

func compare(operator conditionOperator, column ColumnRef, value any) Condition {
	checkColumn(column)
	switch operator {
	case operatorEQ, operatorNE, operatorGT, operatorGTE, operatorLT, operatorLTE:
		return conditionNode{operator: operator, column: column, value: cloneRequestData(value)}
	default:
		panicCore("比较操作符 %q 无效", operator)
		return nil
	}
}

func newJoin(joinType JoinType, entity any, alias string, on Condition) JoinOp {
	join := JoinOp{Entity: entity, Alias: alias, Condition: on, Type: joinType}
	checkJoin(join)

	return join
}

func newOrder(column ColumnRef, direction Direction) Order {
	order := Order{Column: column, Direction: direction}
	checkOrder(order)

	return order
}

func checkColumn(column ColumnRef) {
	if !queryNamePattern.MatchString(column.name) {
		panicCore("字段引用无效")
	}
	if column.entityType != nil {
		checkEntityType(column.entityType)
	}
	if column.alias != "" {
		checkName("字段别名", column.alias)
	}
}

func checkEntityType(entityType reflect.Type) {
	if entityType == nil || entityType.Kind() != reflect.Struct || entityType.Name() == "" || entityType.PkgPath() == "" {
		panicCore("实体必须是非指针具名 struct")
	}
}

func checkJoin(join JoinOp) {
	if join.Type != JoinLeft && join.Type != JoinInner {
		panicCore("关联类型 %q 无效", join.Type)
	}
	checkEntityType(reflect.TypeOf(join.Entity))
	checkName("实体别名", join.Alias)
	condition, ok := join.Condition.(conditionNode)
	if !ok || condition.operator != operatorOn {
		panicCore("关联条件必须使用 On")
	}
}

func checkOrder(order Order) {
	checkColumn(order.Column)
	if order.Direction != Ascending && order.Direction != Descending {
		panicCore("排序方向 %q 无效", order.Direction)
	}
}

func checkName(role string, name string) {
	if !queryNamePattern.MatchString(name) {
		panicCore("%s %q 无效", role, name)
	}
}
