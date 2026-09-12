package codegen

import (
	"context"
	"go/ast"
	"go/constant"
	"go/types"

	"github.com/toothdy/cool-admin-go-next/cool-next/outbox"
)

type consumerMetadata struct {
	messageType string
	name        string
	topic       string
	versions    []uint32
}

func (a *analysis) consumerMetadata(
	pkg *loadedPackage,
	function *ast.FuncDecl,
) (consumerMetadata, bool) {
	var calls []*ast.CallExpr
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector := calledSelector(call.Fun)
		if selector == nil {
			return true
		}
		object, _ := pkg.packageInfo.TypesInfo.Uses[selector.Sel].(*types.Func)
		if object != nil && object.Pkg() != nil && object.Pkg().Path() == outboxPackagePath && object.Name() == "Consume" {
			calls = append(calls, call)
		}
		return true
	})
	position := a.position(pkg, function.Pos())
	if len(calls) != 1 {
		a.add("CG105", "Consumer 构造器必须直接包含唯一 outbox.Consume 调用", position)
		return consumerMetadata{}, false
	}
	call := calls[0]
	if len(call.Args) != 5 {
		a.add("CG105", "Consumer 构造器的 outbox.Consume 参数无效", a.position(pkg, call.Pos()))
		return consumerMetadata{}, false
	}
	name, nameOK := consumerConst(pkg, call.Args[0])
	topic, topicOK := consumerConst(pkg, call.Args[1])
	messageType, messageTypeOK := consumerConst(pkg, call.Args[2])
	versions, versionsOK := constantVersions(pkg, call.Args[3])
	if !nameOK || !topicOK || !messageTypeOK || !versionsOK {
		a.add("CG105", "Consumer Name、Topic、Message Type 和版本必须是生成期常量", a.position(pkg, call.Pos()))
		return consumerMetadata{}, false
	}
	definition, err := outbox.Consume(
		name,
		topic,
		messageType,
		versions,
		func(context.Context, outbox.Incoming[struct{}]) error { return nil },
	)
	if err != nil {
		a.add("CG105", "Consumer 元数据无效: "+err.Error(), a.position(pkg, call.Pos()))
		return consumerMetadata{}, false
	}
	subscription, err := outbox.NewSubscription(definition)
	if err != nil {
		a.add("CG105", "Consumer 元数据无效: "+err.Error(), a.position(pkg, call.Pos()))
		return consumerMetadata{}, false
	}

	return consumerMetadata{
		messageType: subscription.MessageType(),
		name:        subscription.Name(),
		topic:       subscription.Topic(),
		versions:    subscription.SupportedVersions(),
	}, true
}

func (a *analysis) checkConsumerNames(model *Model) {
	seen := make(map[string]Position)
	for _, current := range model.modules {
		for _, constructor := range current.constructors {
			if !constructor.isConsumerDef {
				continue
			}
			if _, exists := seen[constructor.consumerName]; exists {
				a.add("CG106", "Consumer Name 全局重复: "+constructor.consumerName, constructor.position)
				continue
			}
			seen[constructor.consumerName] = constructor.position
		}
	}
}

func calledSelector(expression ast.Expr) *ast.SelectorExpr {
	switch current := expression.(type) {
	case *ast.SelectorExpr:
		return current
	case *ast.IndexExpr:
		return calledSelector(current.X)
	case *ast.IndexListExpr:
		return calledSelector(current.X)
	default:
		return nil
	}
}

func consumerConst(pkg *loadedPackage, expression ast.Expr) (string, bool) {
	value := pkg.packageInfo.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func constantVersions(pkg *loadedPackage, expression ast.Expr) ([]uint32, bool) {
	literal, ok := expression.(*ast.CompositeLit)
	if !ok || len(literal.Elts) == 0 {
		return nil, false
	}
	versions := make([]uint32, len(literal.Elts))
	for index, element := range literal.Elts {
		value := pkg.packageInfo.TypesInfo.Types[element].Value
		if value == nil {
			return nil, false
		}
		version, exact := constant.Uint64Val(value)
		if !exact || version == 0 || version > uint64(^uint32(0)) {
			return nil, false
		}
		versions[index] = uint32(version)
	}
	return versions, true
}
