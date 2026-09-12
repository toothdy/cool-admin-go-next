package crud

import (
	"context"
	"regexp"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

var (
	tableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	// 比较运算符映射:同模板 "column OP ?",只换符号
	compareOperators = map[conditionOperator]string{
		operatorGT:  ">",
		operatorGTE: ">=",
		operatorLT:  "<",
		operatorLTE: "<=",
	}
)

// 将查询计划应用到克隆后的 Model
func applyQueryPlan(ctx context.Context, model *gdb.Model, plan *QueryPlan) (*gdb.Model, error) {
	if ctx == nil {
		return nil, exception.Core("查询上下文不能为空")
	}
	if model == nil {
		return nil, exception.Core("查询 Model 不能为空")
	}
	if plan == nil {
		return nil, exception.Core("查询计划不能为空")
	}
	if err := checkPlan(plan); err != nil {
		return nil, err
	}

	applied := model.Clone().Ctx(ctx)
	applied = applied.As(applied.QuoteWord(plan.root.alias))
	for _, join := range plan.joins {
		condition := quoteColumn(applied, join.left) + " = " + quoteColumn(applied, join.right)
		switch join.joinType {
		case JoinLeft:
			applied = applied.LeftJoin(join.table, join.alias, condition)
		case JoinInner:
			applied = applied.InnerJoin(join.table, join.alias, condition)
		}
	}
	for _, condition := range plan.where {
		expression, arguments := formatCondition(applied, condition)
		applied = applied.Where(expression, arguments...)
	}

	fields := make([]string, len(plan.selects))
	for index, selectField := range plan.selects {
		fields[index] = quoteColumn(applied, selectField.column) + " AS " + applied.QuoteWord(selectField.alias)
	}
	applied = applied.Fields(gdb.Raw(strings.Join(fields, ", ")))

	if len(plan.groupBy) > 0 {
		groupBy := make([]string, len(plan.groupBy))
		for index, column := range plan.groupBy {
			groupBy[index] = quoteColumn(applied, column)
		}
		applied = applied.Group(groupBy...)
	}
	if len(plan.having) > 0 {
		parts := make([]string, len(plan.having))
		var arguments []any
		for index, condition := range plan.having {
			expression, conditionArguments := formatCondition(applied, condition)
			parts[index] = "(" + expression + ")"
			arguments = append(arguments, conditionArguments...)
		}
		applied = applied.Having(strings.Join(parts, " AND "), arguments...)
	}
	for _, order := range plan.orderBy {
		applied = applied.Order(quoteColumn(applied, order.column), string(order.direction))
	}

	return applied, nil
}

// 查询计划的执行节点
func checkPlan(plan *QueryPlan) error {
	if !tableNamePattern.MatchString(plan.root.table) || plan.root.alias != rootQueryAlias {
		return exception.Core("查询计划根实体无效")
	}
	if len(plan.selects) == 0 {
		return exception.Core("查询计划投影不能为空")
	}
	for _, join := range plan.joins {
		if !tableNamePattern.MatchString(join.table) || !queryNamePattern.MatchString(join.alias) {
			return exception.Core("查询计划关联实体无效")
		}
		if join.joinType != JoinLeft && join.joinType != JoinInner {
			return exception.Core("查询计划关联类型无效")
		}
		if !isValidColumn(join.left) || !isValidColumn(join.right) {
			return exception.Core("查询计划关联字段无效")
		}
	}
	for _, selectField := range plan.selects {
		if !isValidColumn(selectField.column) || !queryNamePattern.MatchString(selectField.alias) {
			return exception.Core("查询计划投影字段无效")
		}
	}
	for _, condition := range plan.where {
		if err := checkCondition(condition); err != nil {
			return err
		}
	}
	for _, column := range plan.groupBy {
		if !isValidColumn(column) {
			return exception.Core("查询计划分组字段无效")
		}
	}
	for _, condition := range plan.having {
		if err := checkCondition(condition); err != nil {
			return err
		}
	}
	for _, order := range plan.orderBy {
		if !isValidColumn(order.column) || (order.direction != Ascending && order.direction != Descending) {
			return exception.Core("查询计划排序节点无效")
		}
	}

	return nil
}

// 查询计划条件
func checkCondition(condition planCondition) error {
	switch condition.operator {
	case operatorEQ, operatorNE:
		if !isValidColumn(condition.column) {
			return exception.Core("查询计划条件字段无效")
		}
	case operatorGT, operatorGTE, operatorLT, operatorLTE:
		if !isValidColumn(condition.column) || condition.value == nil {
			return exception.Core("查询计划比较条件无效")
		}
	case operatorIn:
		values, matches := condition.value.([]any)
		if !isValidColumn(condition.column) || !matches || len(values) == 0 {
			return exception.Core("查询计划集合条件无效")
		}
		for _, value := range values {
			if value == nil {
				return exception.Core("查询计划集合条件无效")
			}
		}
	case operatorLike:
		if _, matches := condition.value.(string); !isValidColumn(condition.column) || !matches {
			return exception.Core("查询计划模糊条件无效")
		}
	case operatorKeyword:
		if len(condition.columns) == 0 || len(condition.columns) != len(condition.arguments) {
			return exception.Core("查询计划关键词条件无效")
		}
		for index, column := range condition.columns {
			if !isValidColumn(column) {
				return exception.Core("查询计划关键词字段无效")
			}
			if _, matches := condition.arguments[index].(string); !matches {
				return exception.Core("查询计划关键词参数无效")
			}
		}
	case operatorRaw:
		if strings.TrimSpace(condition.expression) == "" {
			return exception.Core("查询计划原始条件无效")
		}
	default:
		return exception.Core("查询计划条件操作符无效")
	}

	return nil
}

// 参数化查询条件
func formatCondition(model *gdb.Model, condition planCondition) (string, []any) {
	column := quoteColumn(model, condition.column)
	switch condition.operator {
	case operatorEQ:
		if condition.value == nil {
			return column + " IS NULL", nil
		}
		return column + " = ?", []any{clonePlanValue(condition.value)}
	case operatorNE:
		if condition.value == nil {
			return column + " IS NOT NULL", nil
		}
		return column + " <> ?", []any{clonePlanValue(condition.value)}
	case operatorGT, operatorGTE, operatorLT, operatorLTE:
		return column + " " + compareOperators[condition.operator] + " ?", []any{clonePlanValue(condition.value)}
	case operatorIn:
		return column + " IN (?)", []any{clonePlanValue(condition.value)}
	case operatorLike:
		return column + " LIKE ?", []any{clonePlanValue(condition.value)}
	case operatorKeyword:
		parts := make([]string, len(condition.columns))
		for index, keywordColumn := range condition.columns {
			parts[index] = quoteColumn(model, keywordColumn) + " LIKE ?"
		}
		return "(" + strings.Join(parts, " OR ") + ")", cloneArgs(condition.arguments)
	case operatorRaw:
		return condition.expression, cloneArgs(condition.arguments)
	default:
		return "", nil
	}
}

// 引用查询计划字段
func quoteColumn(model *gdb.Model, column planColumn) string {
	return model.QuoteWord(column.alias) + "." + model.QuoteWord(column.column)
}

// 查询计划字段
func isValidColumn(column planColumn) bool {
	return tableNamePattern.MatchString(column.table) &&
		queryNamePattern.MatchString(column.alias) &&
		queryNamePattern.MatchString(column.column)
}
