package codegen

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
)

const modulePackagePath = "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

func (a *analysis) analyzeConfig(pkg *loadedPackage, configFile, root string) (ConfigDeclaration, []Reference) {
	file := pkg.syntax[configFile]
	if file == nil {
		a.add("CG009", "无法读取模块配置语法树", positionFromPath(a.dir, configFile))
		return ConfigDeclaration{}, nil
	}
	var declarations []*ast.FuncDecl
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "ModuleConfig" && function.Recv == nil {
			declarations = append(declarations, function)
		}
	}
	if len(declarations) != 1 {
		a.add("CG010", "模块必须精确声明一个 ModuleConfig", positionFromPath(a.dir, configFile))
		return ConfigDeclaration{}, nil
	}
	function := declarations[0]
	object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
	if object == nil {
		a.add("CG011", "ModuleConfig 类型信息缺失", a.position(pkg, function.Pos()))
		return ConfigDeclaration{}, nil
	}
	signature, _ := object.Type().(*types.Signature)
	if signature == nil || signature.TypeParams().Len() != 0 || signature.Params().Len() != 0 || signature.Results().Len() != 1 {
		a.add("CG011", "ModuleConfig 签名无效", a.position(pkg, function.Pos()))
		return ConfigDeclaration{}, nil
	}
	named, configType := declarationType(signature.Results().At(0).Type())
	if named == nil || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != modulePackagePath || named.Obj().Name() != "Declaration" || !isLocalStruct(configType, pkg.packageInfo.Types) {
		a.add("CG011", "ModuleConfig 必须返回 module.Declaration[Config]", a.position(pkg, function.Pos()))
		return ConfigDeclaration{}, nil
	}
	if function.Body == nil || len(function.Body.List) != 1 {
		a.add("CG012", "ModuleConfig 必须返回静态 Declaration 字面量", a.position(pkg, function.Pos()))
		return ConfigDeclaration{}, nil
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		a.add("CG012", "ModuleConfig 必须返回静态 Declaration 字面量", a.position(pkg, function.Pos()))
		return ConfigDeclaration{}, nil
	}
	literal, ok := returned.Results[0].(*ast.CompositeLit)
	if !ok {
		a.add("CG012", "ModuleConfig 必须返回静态 Declaration 字面量", a.position(pkg, returned.Pos()))
		return ConfigDeclaration{}, nil
	}
	if invalid := invalidDecl(literal); invalid.IsValid() {
		a.add("CG012", "ModuleConfig 必须返回命名字段的静态 Declaration 字面量", a.position(pkg, invalid))
		return ConfigDeclaration{}, nil
	}
	references := a.findReferences(pkg, root, literal)
	return ConfigDeclaration{
		description: declarationText(pkg, literal, "Description"),
		name:        declarationText(pkg, literal, "Name"),
		packageName: pkg.packageInfo.Name,
		packagePath: pkg.packageInfo.PkgPath,
		typeName:    configType.Obj().Name(),
		position:    a.position(pkg, function.Pos()),
		typ:         configType,
		order:       declarationOrder(pkg, literal),
	}, references
}

func declarationText(pkg *loadedPackage, literal *ast.CompositeLit, fieldName string) string {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, named := field.Key.(*ast.Ident)
		if !named || name.Name != fieldName {
			continue
		}
		value := pkg.packageInfo.TypesInfo.Types[field.Value].Value
		if value == nil || value.Kind() != constant.String {
			return ""
		}
		return constant.StringVal(value)
	}
	return ""
}

func declarationOrder(pkg *loadedPackage, literal *ast.CompositeLit) int {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		name, named := field.Key.(*ast.Ident)
		if !ok || !named || name.Name != "Order" {
			continue
		}
		value := pkg.packageInfo.TypesInfo.Types[field.Value].Value
		if value == nil || value.Kind() != constant.Int {
			return 0
		}
		parsed, ok := constant.Int64Val(value)
		if !ok {
			return 0
		}
		return int(parsed)
	}
	return 0
}

func invalidDecl(literal *ast.CompositeLit) token.Pos {
	for _, element := range literal.Elts {
		if element == nil {
			return literal.Pos()
		}
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return element.Pos()
		}
		if field.Key == nil {
			return element.Pos()
		}
		if _, ok := field.Key.(*ast.Ident); !ok {
			return field.Key.Pos()
		}
	}
	return token.NoPos
}

func declarationType(value types.Type) (*types.Named, *types.Named) {
	named, ok := types.Unalias(value).(*types.Named)
	if !ok || named.TypeArgs().Len() != 1 {
		return nil, nil
	}
	config, ok := types.Unalias(named.TypeArgs().At(0)).(*types.Named)
	return named, config
}

func isLocalStruct(named *types.Named, current *types.Package) bool {
	return named != nil && named.Obj().Pkg() == current && named.Obj().Exported() && func() bool { _, ok := named.Underlying().(*types.Struct); return ok }()
}

func (a *analysis) findReferences(pkg *loadedPackage, root string, literal *ast.CompositeLit) []Reference {
	var references []Reference
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok || (name.Name != "Middlewares" && name.Name != "GlobalMiddlewares") {
			continue
		}
		values, ok := field.Value.(*ast.CompositeLit)
		if !ok {
			a.add("CG013", "中间件引用必须是静态列表", a.position(pkg, field.Pos()))
			continue
		}
		for _, value := range values.Elts {
			call, ok := value.(*ast.CallExpr)
			if !ok {
				a.add("CG014", "中间件引用必须是 module.Ref 调用", a.position(pkg, value.Pos()))
				continue
			}
			reference, ok := a.resolveReference(pkg, root, name.Name, call)
			if ok {
				references = append(references, reference)
			}
		}
	}
	return references
}

func (a *analysis) resolveReference(pkg *loadedPackage, _, group string, call *ast.CallExpr) (Reference, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) != 1 {
		a.add("CG014", "中间件引用必须是 module.Ref 调用", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	function, _ := pkg.packageInfo.TypesInfo.Uses[selector.Sel].(*types.Func)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != modulePackagePath || function.Name() != "Ref" {
		a.add("CG014", "中间件引用必须是 module.Ref 调用", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	value := pkg.packageInfo.TypesInfo.Types[call.Args[0]].Value
	if value == nil || value.Kind() != constant.String {
		a.add("CG015", "module.Ref 参数必须是常量字符串", a.position(pkg, call.Args[0].Pos()))
		return Reference{}, false
	}
	symbol := constant.StringVal(value)
	parts := strings.Split(symbol, ".")
	if len(parts) < 2 {
		a.add("CG016", "module.Ref 符号路径无效", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	targetPackage := pkg.packageInfo.PkgPath + "/" + strings.Join(parts[:len(parts)-1], "/")
	target := a.packages.byPath[targetPackage]
	if target == nil {
		a.add("CG017", "module.Ref 目标包不存在或不属于当前模块", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	object, _ := target.packageInfo.Types.Scope().Lookup(parts[len(parts)-1]).(*types.Func)
	if object == nil {
		a.add("CG018", "module.Ref 目标必须是包级函数", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	if !validMiddleware(object.Type()) {
		a.add("CG104", "module.Ref 目标必须是合法中间件构造器", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	targetPosition := a.position(target, object.Pos())
	if !a.eligible[filepath.Join(a.dir, filepath.FromSlash(targetPosition.File))] {
		a.add("CG019", "module.Ref 不能引用排除文件", a.position(pkg, call.Pos()))
		return Reference{}, false
	}
	return Reference{group: group, position: a.position(pkg, call.Pos()), symbol: symbol, target: targetPosition}, true
}

func (a *analysis) position(pkg *loadedPackage, position token.Pos) Position {
	resolved := pkg.packageInfo.Fset.PositionFor(position, true)
	result := positionFromPath(a.dir, resolved.Filename)
	result.Line = resolved.Line
	result.Column = resolved.Column
	return result
}
