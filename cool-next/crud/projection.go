package crud

import (
	"fmt"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 已解析的查询字段来源
type QueryColumn struct {
	Descriptor gnentity.Metadata
	Field      gnentity.Field
	Source     string
}

// 已解析的请求字段匹配
type QueryMatch struct {
	Column       QueryColumn
	RequestParam string
}

// 已解析的查询输出字段
type QuerySelect struct {
	Column QueryColumn
	Name   string
}

// 静态 QueryOp 的只读字段投影
type QueryProjection struct {
	KeyWordLikeFields []QueryColumn
	FieldEq           []QueryMatch
	FieldLike         []QueryMatch
	Select            []QuerySelect
}

// 解析根实体字段引用
func ProjectColumns(
	resolver DescriptorResolver,
	rootEntity any,
	columns []ColumnRef,
) ([]QueryColumn, error) {
	compiler, err := newPlanCompiler(resolver, rootEntity)
	if err != nil {
		return nil, err
	}
	result := make([]QueryColumn, 0, len(columns))
	for _, reference := range append([]ColumnRef(nil), columns...) {
		column, resolveErr := compiler.resolveColumn(reference)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if column.alias != rootQueryAlias {
			return nil, exception.Core("字段策略只能引用根实体")
		}
		result = append(result, compiler.projectColumn(column))
	}

	return result, nil
}

// 解析静态 QueryOp
func ProjectQuery(
	resolver DescriptorResolver,
	rootEntity any,
	op QueryOp,
) (QueryProjection, error) {
	compiler, err := newPlanCompiler(resolver, rootEntity)
	if err != nil {
		return QueryProjection{}, err
	}
	if len(op.Join) > maxQueryPlanJoins {
		return QueryProjection{}, exception.Core("关联查询数量超过上限")
	}
	for _, join := range append([]JoinOp(nil), op.Join...) {
		if err = compiler.compileJoin(join); err != nil {
			return QueryProjection{}, err
		}
	}
	selects := append([]SelectField(nil), op.Select...)
	if err = compiler.compileSelects(selects); err != nil {
		return QueryProjection{}, err
	}

	result := QueryProjection{
		KeyWordLikeFields: make([]QueryColumn, 0, len(op.KeyWordLikeFields)),
		FieldEq:           make([]QueryMatch, 0, len(op.FieldEq)),
		FieldLike:         make([]QueryMatch, 0, len(op.FieldLike)),
		Select:            make([]QuerySelect, 0, len(compiler.plan.selects)),
	}
	for _, reference := range append([]ColumnRef(nil), op.KeyWordLikeFields...) {
		column, resolveErr := compiler.resolveColumn(reference)
		if resolveErr != nil {
			return QueryProjection{}, resolveErr
		}
		if column.logicalType != gnentity.LogicalString {
			return QueryProjection{}, exception.Core(fmt.Sprintf("字段 %s 不支持关键词查询", column.name))
		}
		result.KeyWordLikeFields = append(result.KeyWordLikeFields, compiler.projectColumn(column))
	}
	for _, match := range append([]FieldEq(nil), op.FieldEq...) {
		projected, resolveErr := compiler.projectMatch(match, false)
		if resolveErr != nil {
			return QueryProjection{}, resolveErr
		}
		result.FieldEq = append(result.FieldEq, projected)
	}
	for _, match := range append([]FieldLike(nil), op.FieldLike...) {
		projected, resolveErr := compiler.projectMatch(match, true)
		if resolveErr != nil {
			return QueryProjection{}, resolveErr
		}
		result.FieldLike = append(result.FieldLike, projected)
	}
	for _, selected := range compiler.plan.selects {
		result.Select = append(result.Select, QuerySelect{
			Column: compiler.projectColumn(selected.column),
			Name:   selected.alias,
		})
	}

	return result, nil
}

func (compiler *queryPlanCompiler) projectMatch(match FieldMatch, isLike bool) (QueryMatch, error) {
	label := "等值"
	if isLike {
		label = "模糊"
	}
	if !queryNamePattern.MatchString(match.RequestParam) {
		return QueryMatch{}, exception.Core(label + "查询请求参数名无效")
	}
	column, err := compiler.resolveColumn(match.Column)
	if err != nil {
		return QueryMatch{}, err
	}
	if isLike && column.logicalType != gnentity.LogicalString {
		return QueryMatch{}, exception.Core(fmt.Sprintf("字段 %s 不支持模糊查询", column.name))
	}

	return QueryMatch{
		Column:       compiler.projectColumn(column),
		RequestParam: match.RequestParam,
	}, nil
}

func (compiler *queryPlanCompiler) projectColumn(column planColumn) QueryColumn {
	metadata := compiler.aliases[column.alias].metadata
	field, _ := metadata.Field(column.name)

	return QueryColumn{
		Descriptor: metadata,
		Field:      field,
		Source:     column.alias + "." + column.jsonName,
	}
}
