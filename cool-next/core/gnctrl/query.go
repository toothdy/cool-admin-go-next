package gnctrl

import (
	"context"
	"errors"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

// 排序方向别名
type Direction = crud.Direction

// 列引用别名
type ColumnRef = crud.ColumnRef

// 查询请求别名
type QueryRequest = crud.QueryRequest

// 查询字段值别名
type RequestValue = crud.RequestValue

// 查询操作别名
type QueryOp = crud.QueryOp

// 字段匹配别名
type FieldMatch = crud.FieldMatch

// 等值请求匹配别名
type FieldEq = crud.FieldEq

// 模糊请求匹配别名
type FieldLike = crud.FieldLike

// 关联操作别名
type JoinOp = crud.JoinOp

// 关联类型别名
type JoinType = crud.JoinType

// 排序节点别名
type Order = crud.Order

// 查询扩展器别名
type QueryExtender = crud.QueryExtender

// WHERE 条件提供器别名
type WhereProvider = crud.WhereProvider

// 查询条件别名
type Condition = crud.Condition

// 查询字段别名
type SelectField = crud.SelectField

// 查询构造器别名
type QueryBuilder = crud.QueryBuilder

const (
	Ascending  = crud.Ascending  // 升序
	Descending = crud.Descending // 降序
	JoinLeft   = crud.JoinLeft   // 左连接
	JoinInner  = crud.JoinInner  // 内连接
)

// 静态或动态查询配置
type QueryProvider interface {
	queryProvider()
}

type staticQueryProvider struct {
	op QueryOp
}

type dynamicProvider struct {
	resolve func(context.Context) (QueryOp, error)
}

func (staticQueryProvider) queryProvider() {}
func (dynamicProvider) queryProvider()     {}

// 静态查询配置
func StaticQuery(op QueryOp) QueryProvider {
	return staticQueryProvider{op: cloneQueryOp(op)}
}

// 动态查询配置
func DynamicQuery(resolve func(context.Context) (QueryOp, error)) QueryProvider {
	if resolve == nil {
		panicCore("动态查询函数不能为空")
	}

	return dynamicProvider{resolve: resolve}
}

// 构造根实体字段引用
func Field(name string) ColumnRef {
	return crud.NewColumnRef(name)
}

// 指定实体字段引用
func FieldOf[E any](name string) ColumnRef {
	return crud.NewColumnRefOf[E](name)
}

// 拼接 AND 条件
func Where(conditions ...Condition) WhereProvider {
	return crud.Where(conditions...)
}

// 等值条件
func EqValue(column ColumnRef, value any) Condition {
	return crud.EqValue(column, value)
}

// 不等条件
func NeValue(column ColumnRef, value any) Condition {
	return crud.NeValue(column, value)
}

// 集合条件
func In(column ColumnRef, values any) Condition {
	return crud.In(column, values)
}

// 模糊匹配条件
func LikeValue(column ColumnRef, value string) Condition {
	return crud.LikeValue(column, value)
}

// 原始参数化条件
func RawWhere(expression string, arguments ...any) Condition {
	return crud.RawWhere(expression, arguments...)
}

// 字段关联条件
func On(left ColumnRef, right ColumnRef) Condition {
	return crud.On(left, right)
}

// 别名实体全部字段
func All(alias string) SelectField {
	return crud.All(alias)
}

// 字段并指定输出别名
func As(column ColumnRef, alias string) SelectField {
	return crud.As(column, alias)
}

// 左关联
func LeftJoin(entity any, alias string, on Condition) JoinOp {
	return crud.LeftJoin(entity, alias, on)
}

// 内关联
func InnerJoin(entity any, alias string, on Condition) JoinOp {
	return crud.InnerJoin(entity, alias, on)
}

// 升序节点
func Asc(column ColumnRef) Order {
	return crud.Asc(column)
}

// 降序节点
func Desc(column ColumnRef) Order {
	return crud.Desc(column)
}

// 默认等值请求匹配
func Eq(column ColumnRef) FieldEq {
	return crud.Eq(column)
}

// 指定参数的等值请求匹配
func EqFrom(column ColumnRef, requestParam string) FieldEq {
	return crud.EqFrom(column, requestParam)
}

// 默认模糊请求匹配
func Like(column ColumnRef) FieldLike {
	return crud.Like(column)
}

// 指定参数的模糊请求匹配
func LikeFrom(column ColumnRef, requestParam string) FieldLike {
	return crud.LikeFrom(column, requestParam)
}

// 查询追加函数
func Extend(extender QueryExtender) QueryExtender {
	return crud.Extend(extender)
}

// 普通查询请求字段
func RequestField(name string, value any) RequestValue {
	return crud.RequestField(name, value)
}

// 显式 null 查询请求字段
func RequestNull(name string) RequestValue {
	return crud.RequestNull(name)
}

// 不可变查询请求
func NewQueryRequest(values []RequestValue) (*QueryRequest, error) {
	return crud.NewQueryRequest(values)
}

func resolveProvider(ctx context.Context, provider QueryProvider) (QueryOp, error) {
	switch current := provider.(type) {
	case nil:
		return crud.StaticShape(QueryOp{}), nil
	case staticQueryProvider:
		return crud.StaticShape(cloneQueryOp(current.op)), nil
	case dynamicProvider:
		op, err := current.resolve(ctx)
		if err != nil {
			var coolError *exception.BaseException
			if errors.As(err, &coolError) {
				return QueryOp{}, err
			}

			return QueryOp{}, exception.WrapCore(err, "解析动态查询配置失败")
		}

		return crud.DynamicShape(cloneQueryOp(op)), nil
	default:
		return QueryOp{}, exception.Core("QueryProvider 无效")
	}
}

func requireProvider(provider QueryProvider) {
	switch provider.(type) {
	case staticQueryProvider, dynamicProvider:
		return
	default:
		panicCore("QueryProvider 无效")
	}
}

func cloneProvider(provider QueryProvider) QueryProvider {
	switch current := provider.(type) {
	case nil:
		return nil
	case staticQueryProvider:
		return staticQueryProvider{op: cloneQueryOp(current.op)}
	case dynamicProvider:
		return current
	default:
		panicCore("QueryProvider 无效")
		return nil
	}
}

func cloneQueryOp(source QueryOp) QueryOp {
	result := source
	result.KeyWordLikeFields = append([]ColumnRef(nil), source.KeyWordLikeFields...)
	result.Select = append([]SelectField(nil), source.Select...)
	result.FieldLike = append([]FieldLike(nil), source.FieldLike...)
	result.FieldEq = append([]FieldEq(nil), source.FieldEq...)
	result.AddOrderBy = append([]Order(nil), source.AddOrderBy...)
	result.Join = append([]JoinOp(nil), source.Join...)

	return result
}
