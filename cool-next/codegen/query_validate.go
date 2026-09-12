package codegen

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"strings"
)

const queryPackagePath = "github.com/toothdy/cool-admin-go-next/cool-next/crud"

type queryArgumentKind uint8

const (
	queryNameArgument queryArgumentKind = iota
	rawWhereArg
)

func (a *analysis) analyzeQueryDSL(root string) {
	for fileName := range a.eligible {
		relative, err := filepath.Rel(root, fileName)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		pkg := a.packages.byFile[fileName]
		if pkg == nil || pkg.syntax[fileName] == nil {
			continue
		}
		var stack []ast.Node
		ast.Inspect(pkg.syntax[fileName], func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			call, ok := node.(*ast.CallExpr)
			if ok {
				a.checkQueryCall(pkg, call)
				a.checkNativeCall(pkg, call)
			}
			selector, ok := node.(*ast.SelectorExpr)
			if ok {
				a.checkDBBypass(pkg, selector)
			}
			identifier, ok := node.(*ast.Ident)
			if ok {
				a.checkQueryRef(pkg, stack, identifier)
				a.checkNativeRef(pkg, stack, identifier)
			}
			stack = append(stack, node)
			return true
		})
	}
}

func (a *analysis) checkNativeCall(pkg *loadedPackage, call *ast.CallExpr) {
	function := calledFunction(pkg.packageInfo.TypesInfo, call.Fun)
	if !packageFunction(function, servicePackagePath, "NativeSQL") || len(call.Args) == 0 {
		return
	}
	expression := call.Args[0]
	value := pkg.packageInfo.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.String || strings.TrimSpace(constant.StringVal(value)) == "" {
		a.add("CG098", "NativeSQL 语句必须是非空常量字符串", a.position(pkg, expression.Pos()))
	}
}

func (a *analysis) checkNativeRef(pkg *loadedPackage, stack []ast.Node, identifier *ast.Ident) {
	function, _ := pkg.packageInfo.TypesInfo.Uses[identifier].(*types.Func)
	if !packageFunction(function, servicePackagePath, "NativeSQL") || directQueryCall(stack, identifier) {
		return
	}
	a.add("CG098", "NativeSQL 只能使用常量语句直接调用", a.position(pkg, identifier.Pos()))
}

func (a *analysis) checkDBBypass(pkg *loadedPackage, selector *ast.SelectorExpr) {
	receiver := types.TypeString(pkg.packageInfo.TypesInfo.TypeOf(selector.X), func(current *types.Package) string {
		return current.Path()
	})
	forbidden := receiver == "*"+gdbPackagePath+".Model" &&
		(selector.Sel.Name == "Raw" || selector.Sel.Name == "DB" || selector.Sel.Name == "TX" || selector.Sel.Name == "Unscoped") ||
		receiver == gdbPackagePath+".TX" && (selector.Sel.Name == "Exec" || selector.Sel.Name == "Query")
	if forbidden {
		a.add("CG099", "业务模块禁止调用 gdb."+selector.Sel.Name, a.position(pkg, selector.Sel.Pos()))
	}
}

func (a *analysis) checkQueryRef(pkg *loadedPackage, stack []ast.Node, identifier *ast.Ident) {
	function, _ := pkg.packageInfo.TypesInfo.Uses[identifier].(*types.Func)
	if !isQueryFunction(function, "RawWhere") || directQueryCall(stack, identifier) {
		return
	}
	a.add("CG065", "RawWhere 只能使用常量表达式直接调用", a.position(pkg, identifier.Pos()))
}

func (a *analysis) checkQueryCall(pkg *loadedPackage, call *ast.CallExpr) {
	function := calledFunction(pkg.packageInfo.TypesInfo, call.Fun)
	argumentIndex, kind, ok := queryArgument(function)
	if !ok || argumentIndex >= len(call.Args) {
		return
	}
	expression := call.Args[argumentIndex]
	value := pkg.packageInfo.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		if kind == rawWhereArg {
			a.add("CG065", "RawWhere 表达式必须是非空常量字符串", a.position(pkg, expression.Pos()))
			return
		}
		a.add("CG066", function.Name()+" 名称必须是常量字符串", a.position(pkg, expression.Pos()))
		return
	}
	text := constant.StringVal(value)
	if kind == rawWhereArg {
		if strings.TrimSpace(text) == "" {
			a.add("CG065", "RawWhere 表达式必须是非空常量字符串", a.position(pkg, expression.Pos()))
		}
		return
	}
	if !codegenLowerCamel.MatchString(text) {
		a.add("CG067", function.Name()+" 名称必须是 lowerCamelCase", a.position(pkg, expression.Pos()))
	}
}

func calledFunction(info *types.Info, expression ast.Expr) *types.Func {
	switch current := expression.(type) {
	case *ast.ParenExpr:
		return calledFunction(info, current.X)
	case *ast.IndexExpr:
		return calledFunction(info, current.X)
	case *ast.IndexListExpr:
		return calledFunction(info, current.X)
	case *ast.Ident:
		function, _ := info.Uses[current].(*types.Func)
		return function
	case *ast.SelectorExpr:
		if selection := info.Selections[current]; selection != nil {
			function, _ := selection.Obj().(*types.Func)
			return function
		}
		function, _ := info.Uses[current.Sel].(*types.Func)
		return function
	default:
		return nil
	}
}

func queryArgument(function *types.Func) (int, queryArgumentKind, bool) {
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != queryPackagePath {
		return 0, 0, false
	}
	switch function.Name() {
	case "RawWhere":
		return 0, rawWhereArg, true
	case "NewColumnRef", "NewColumnRefOf", "All":
		return 0, queryNameArgument, true
	case "EqFrom", "LikeFrom", "As", "LeftJoin", "InnerJoin":
		return 1, queryNameArgument, true
	case "Of":
		if columnRefMethod(function) {
			return 0, queryNameArgument, true
		}
	}
	return 0, 0, false
}

func isQueryFunction(function *types.Func, name string) bool {
	return packageFunction(function, queryPackagePath, name)
}

func packageFunction(function *types.Func, packagePath, name string) bool {
	return function != nil && function.Pkg() != nil && function.Pkg().Path() == packagePath && function.Name() == name
}

func directQueryCall(stack []ast.Node, expression ast.Expr) bool {
	current := expression
	for index := len(stack) - 1; index >= 0; index-- {
		switch parent := stack[index].(type) {
		case *ast.SelectorExpr:
			if parent.Sel != current {
				return false
			}
			current = parent
		case *ast.ParenExpr:
			if parent.X != current {
				return false
			}
			current = parent
		case *ast.IndexExpr:
			if parent.X != current {
				return false
			}
			current = parent
		case *ast.IndexListExpr:
			if parent.X != current {
				return false
			}
			current = parent
		case *ast.CallExpr:
			return parent.Fun == current
		default:
			return false
		}
	}
	return false
}

func columnRefMethod(function *types.Func) bool {
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return false
	}
	named, _ := types.Unalias(signature.Recv().Type()).(*types.Named)
	return named != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == queryPackagePath && named.Obj().Name() == "ColumnRef"
}
