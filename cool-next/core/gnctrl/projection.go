package gnctrl

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
)

// Controller Definition 的独立只读快照
type DefinitionSnapshot struct {
	Area    Area
	Path    string
	Options RouterOptions
	Curd    *CurdOption
	Routes  []Route
}

// 复制 Controller Definition 的静态声明
func Snapshot(value Definition) (DefinitionSnapshot, error) {
	current, err := requireDefinition(value)
	if err != nil {
		return DefinitionSnapshot{}, err
	}
	result := DefinitionSnapshot{
		Area:    current.area,
		Path:    current.path,
		Options: cloneOptions(current.options),
		Routes:  cloneRoutes(current.routes),
	}
	if current.curd != nil {
		option := cloneCurdOption(*current.curd)
		result.Curd = &option
	}

	return result, nil
}

// 投影静态查询并报告是否支持静态投影
func ProjectQuery(
	provider QueryProvider,
	resolver crud.DescriptorResolver,
	entity any,
) (crud.QueryProjection, bool, error) {
	switch current := provider.(type) {
	case nil:
		projection, err := crud.ProjectQuery(resolver, entity, QueryOp{})
		return projection, true, err
	case staticQueryProvider:
		projection, err := crud.ProjectQuery(resolver, entity, cloneQueryOp(current.op))
		return projection, true, err
	case dynamicProvider:
		return crud.QueryProjection{}, false, nil
	default:
		return crud.QueryProjection{}, false, exception.Core("QueryProvider 无效")
	}
}
