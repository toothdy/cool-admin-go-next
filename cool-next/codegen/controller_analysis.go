package codegen

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/route"
)

const gnctrlPath = "github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"

func (a *analysis) analyzeControllers(
	root string,
	identity module.Identity,
	entities []EntityDeclaration,
	services []ServiceDeclaration,
) []ControllerDeclaration {
	var result []ControllerDeclaration
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			controllerArea, relative, exists := controllerSource(root, fileName)
			if !a.eligible[fileName] || !exists {
				continue
			}
			for _, node := range file.Decls {
				function, matches := node.(*ast.FuncDecl)
				if !matches || function.Recv != nil {
					continue
				}
				object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
				if object == nil {
					continue
				}
				signature, _ := object.Type().(*types.Signature)
				if !returnsController(signature) {
					continue
				}
				if !isControllerFactory(signature) {
					a.add("CG023", "Controller 工厂签名无效", a.position(pkg, function.Pos()))
					continue
				}
				if !function.Name.IsExported() {
					a.add("CG023", "Controller 工厂必须导出", a.position(pkg, function.Name.Pos()))
					continue
				}
				declaration, valid := a.analyzeController(
					root,
					pkg,
					function,
					signature,
					controllerArea,
					relative,
					identity,
					entities,
					services,
				)
				if valid {
					result = append(result, declaration)
				}
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].packagePath != result[right].packagePath {
			return result[left].packagePath < result[right].packagePath
		}
		if result[left].name != result[right].name {
			return result[left].name < result[right].name
		}

		return result[left].path < result[right].path
	})

	return result
}

func (a *analysis) analyzeController(
	root string,
	pkg *loadedPackage,
	function *ast.FuncDecl,
	signature *types.Signature,
	sourceArea ControllerArea,
	relative string,
	identity module.Identity,
	entities []EntityDeclaration,
	services []ServiceDeclaration,
) (ControllerDeclaration, bool) {
	returned := staticReturn(function)
	if returned == nil {
		a.add("CG023", "Controller 工厂必须返回静态 Builder 调用链", a.position(pkg, function.Pos()))
		return ControllerDeclaration{}, false
	}
	chain, valid := parseChain(pkg, returned.Results[0])
	if !valid {
		a.add("CG023", "Controller 工厂必须返回 Admin/App Builder 的 Build 结果", a.position(pkg, returned.Pos()))
		return ControllerDeclaration{}, false
	}
	if chain.curdCount > 1 {
		a.add("CG025", "Controller 不能重复声明 Curd", a.position(pkg, chain.curd.Pos()))
		return ControllerDeclaration{}, false
	}
	if chain.optionsCount > 1 {
		a.add("CG100", "Controller 不能重复声明 Options", a.position(pkg, chain.options.Pos()))
		return ControllerDeclaration{}, false
	}
	if chain.area != sourceArea {
		a.add("CG024", "Controller 声明区域与源码目录不一致", a.position(pkg, chain.factory.Pos()))
		return ControllerDeclaration{}, false
	}
	explicitPath := ""
	if chain.path != nil {
		var exists bool
		explicitPath, exists = controllerConst(pkg, chain.path)
		if !exists || !route.ValidRelativePath(explicitPath) {
			a.add("CG024", "Controller 路径必须是合法常量相对路径", a.position(pkg, chain.path.Pos()))
			return ControllerDeclaration{}, false
		}
	}
	controllerPath := areaPath(sourceArea, explicitPath)
	if explicitPath == "" {
		controllerPath = inferredPath(sourceArea, identity.Key(), relative)
	}
	declaration := ControllerDeclaration{
		area:           sourceArea,
		name:           function.Name.Name,
		packageName:    pkg.packageInfo.Name,
		packagePath:    pkg.packageInfo.PkgPath,
		parameterTypes: parameterTypes(signature),
		path:           controllerPath,
		position:       a.position(pkg, function.Pos()),
		prefix:         controllerPath,
		sensitive:      true,
	}
	if chain.optionsCount == 1 && !a.analyzeOptions(root, pkg, function, chain.options, &declaration) {
		return ControllerDeclaration{}, false
	}
	var literal *ast.CompositeLit
	if chain.curdCount == 1 {
		literal = controllerLiteral(pkg, function, chain.curd.Args[0])
		if literal == nil || !isNamedType(pkg.packageInfo.TypesInfo.TypeOf(literal), gnctrlPath, "CurdOption") || !hasNamedFields(literal) {
			a.add("CG025", "Curd 必须使用当前工厂内的命名字段 CurdOption 字面量", a.position(pkg, chain.curd.Args[0].Pos()))
			return ControllerDeclaration{}, false
		}
		prefix, prefixValid := controllerPrefix(pkg, literal)
		if !prefixValid || !route.ValidRelativePath(prefix) {
			position := literal.Pos()
			if value, exists := literalField(literal, "Prefix"); exists {
				position = value.Pos()
			}
			a.add("CG024", "Curd Prefix 必须是合法常量相对路径", a.position(pkg, position))
			return ControllerDeclaration{}, false
		}
		if prefix != "" {
			declaration.prefix = areaPath(sourceArea, prefix)
		}
		entityExpression, exists := literalField(literal, "Entity")
		entityType, entityValid := resolveEntityType(pkg, function, entityExpression, exists)
		if !entityValid || !slices.ContainsFunc(entities, func(entity EntityDeclaration) bool {
			return types.Identical(types.Unalias(entity.typ), types.Unalias(entityType))
		}) {
			position := literal.Pos()
			if exists {
				position = entityExpression.Pos()
			}
			a.add("CG026", "Curd Entity 必须是当前模块已发现实体的零值", a.position(pkg, position))
			return ControllerDeclaration{}, false
		}
		serviceExpression, exists := literalField(literal, "Service")
		serviceDeclaration, serviceType, serviceValid := controllerService(pkg, signature, serviceExpression, exists, services)
		if !serviceValid {
			position := literal.Pos()
			if exists {
				position = serviceExpression.Pos()
			}
			a.add("CG027", "Curd Service 必须引用工厂参数中的 Base Service 指针", a.position(pkg, position))
			return ControllerDeclaration{}, false
		}
		if !types.Identical(types.Unalias(serviceDeclaration.entityType), types.Unalias(entityType)) ||
			!types.Identical(types.Unalias(serviceDeclaration.idType), types.Typ[types.Uint64]) {
			a.add("CG028", "Curd Entity 必须与 Service Base[E, uint64] 泛型一致", a.position(pkg, serviceExpression.Pos()))
			return ControllerDeclaration{}, false
		}
		insertType, insertValid := resolveInsertType(pkg, function, literal)
		if !insertValid || insertType != nil && !types.Identical(types.Unalias(insertType), types.Unalias(entityType)) {
			position := literal.Pos()
			if value, exists := literalField(literal, "InsertParam"); exists {
				position = value.Pos()
			}
			a.add("CG029", "InsertParam 实体必须与 Curd Entity 一致", a.position(pkg, position))
			return ControllerDeclaration{}, false
		}
		declaration.hasCurd = true
		declaration.entityType = entityType
		declaration.insertType = insertType
		declaration.serviceType = serviceType
	}
	routes, valid := a.analyzeRoutes(root, pkg, function, signature, chain, declaration, literal)
	if !valid {
		return ControllerDeclaration{}, false
	}
	declaration.routes = routes

	return declaration, true
}

type controllerChain struct {
	area         ControllerArea
	curd         *ast.CallExpr
	curdCount    int
	factory      *ast.CallExpr
	options      *ast.CallExpr
	optionsCount int
	path         ast.Expr // nil 表示 Admin()/App() 零参数调用，等价于显式传空字符串
	routes       []*ast.CallExpr
}

func parseChain(pkg *loadedPackage, expression ast.Expr) (controllerChain, bool) {
	build, matches := unparenExpr(expression).(*ast.CallExpr)
	if !matches || len(build.Args) != 0 || !packageFunction(calledFunction(pkg.packageInfo.TypesInfo, build.Fun), gnctrlPath, "Build") {
		return controllerChain{}, false
	}
	selector, matches := unparenExpr(build.Fun).(*ast.SelectorExpr)
	if !matches {
		return controllerChain{}, false
	}
	current := unparenExpr(selector.X)
	chain := controllerChain{}
	for {
		call, isCall := current.(*ast.CallExpr)
		if !isCall {
			return controllerChain{}, false
		}
		function := calledFunction(pkg.packageInfo.TypesInfo, call.Fun)
		if packageFunction(function, gnctrlPath, "Curd") {
			if len(call.Args) != 1 {
				return controllerChain{}, false
			}
			chain.curdCount++
			chain.curd = call
			method, isSelector := unparenExpr(call.Fun).(*ast.SelectorExpr)
			if !isSelector {
				return controllerChain{}, false
			}
			current = unparenExpr(method.X)
			continue
		}
		if packageFunction(function, gnctrlPath, "Options") {
			if len(call.Args) != 1 {
				return controllerChain{}, false
			}
			chain.optionsCount++
			chain.options = call
			method, isSelector := unparenExpr(call.Fun).(*ast.SelectorExpr)
			if !isSelector {
				return controllerChain{}, false
			}
			current = unparenExpr(method.X)
			continue
		}
		if packageFunction(function, gnctrlPath, "Route") {
			if len(call.Args) == 0 {
				return controllerChain{}, false
			}
			chain.routes = append([]*ast.CallExpr{call}, chain.routes...)
			method, isSelector := unparenExpr(call.Fun).(*ast.SelectorExpr)
			if !isSelector {
				return controllerChain{}, false
			}
			current = unparenExpr(method.X)
			continue
		}
		if !packageFunction(function, gnctrlPath, "Admin") && !packageFunction(function, gnctrlPath, "App") || len(call.Args) > 1 {
			return controllerChain{}, false
		}
		chain.factory = call
		if len(call.Args) == 1 {
			chain.path = call.Args[0]
		}
		if function.Name() == "Admin" {
			chain.area = ControllerAdmin
		} else {
			chain.area = ControllerApp
		}

		return chain, true
	}
}

func staticReturn(function *ast.FuncDecl) *ast.ReturnStmt {
	if function.Body == nil || len(function.Body.List) == 0 {
		return nil
	}
	returned, matches := function.Body.List[len(function.Body.List)-1].(*ast.ReturnStmt)
	if !matches || len(returned.Results) != 1 {
		return nil
	}
	count := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			count++
		}

		return true
	})
	if count != 1 {
		return nil
	}

	return returned
}

func isControllerFactory(signature *types.Signature) bool {
	return signature != nil && signature.TypeParams().Len() == 0 && !signature.Variadic() &&
		signature.Results().Len() == 1 && isNamedType(signature.Results().At(0).Type(), gnctrlPath, "Definition")
}

func returnsController(signature *types.Signature) bool {
	if signature == nil || signature.Results().Len() == 0 {
		return false
	}

	return isNamedType(signature.Results().At(0).Type(), gnctrlPath, "Definition")
}

func controllerSource(root, fileName string) (ControllerArea, string, bool) {
	relative, err := filepath.Rel(root, fileName)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 3 || parts[0] != "controller" {
		return "", "", false
	}
	switch parts[1] {
	case string(ControllerAdmin):
		return ControllerAdmin, strings.Join(parts[2:], "/"), true
	case string(ControllerApp):
		return ControllerApp, strings.Join(parts[2:], "/"), true
	default:
		return "", "", false
	}
}

func inferredPath(area ControllerArea, moduleKey, relative string) string {
	file := strings.TrimSuffix(relative, filepath.Ext(relative))

	return path.Join("/", string(area), moduleKey, file)
}

func areaPath(area ControllerArea, relative string) string {
	return path.Join("/", string(area), relative)
}

func controllerConst(pkg *loadedPackage, expression ast.Expr) (string, bool) {
	value := pkg.packageInfo.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}

	return constant.StringVal(value), true
}

func controllerLiteral(pkg *loadedPackage, function *ast.FuncDecl, expression ast.Expr) *ast.CompositeLit {
	resolved := localValue(pkg, function, expression)
	literal, _ := resolved.(*ast.CompositeLit)

	return literal
}

func localValue(pkg *loadedPackage, function *ast.FuncDecl, expression ast.Expr) ast.Expr {
	expression = unparenExpr(expression)
	identifier, matches := expression.(*ast.Ident)
	if !matches {
		return expression
	}
	target := pkg.packageInfo.TypesInfo.Uses[identifier]
	if target == nil || function.Body == nil {
		return nil
	}
	var initializer ast.Expr
	assignments := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt:
			for index, left := range current.Lhs {
				if assignedController(pkg, left) != target {
					continue
				}
				assignments++
				if name, direct := unparenExpr(left).(*ast.Ident); direct && controllerObject(pkg, name) == target && index < len(current.Rhs) {
					initializer = unparenExpr(current.Rhs[index])
				}
			}
		case *ast.IncDecStmt:
			if assignedController(pkg, current.X) == target {
				assignments++
			}
		case *ast.ValueSpec:
			for index, name := range current.Names {
				if controllerObject(pkg, name) != target {
					continue
				}
				assignments++
				if index < len(current.Values) {
					initializer = unparenExpr(current.Values[index])
				}
			}
		}

		return true
	})
	if assignments != 1 {
		return nil
	}

	return initializer
}

func controllerObject(pkg *loadedPackage, identifier *ast.Ident) types.Object {
	if object := pkg.packageInfo.TypesInfo.Defs[identifier]; object != nil {
		return object
	}

	return pkg.packageInfo.TypesInfo.Uses[identifier]
}

func assignedController(pkg *loadedPackage, expression ast.Expr) types.Object {
	switch current := unparenExpr(expression).(type) {
	case *ast.Ident:
		return controllerObject(pkg, current)
	case *ast.SelectorExpr:
		return assignedController(pkg, current.X)
	case *ast.IndexExpr:
		return assignedController(pkg, current.X)
	case *ast.IndexListExpr:
		return assignedController(pkg, current.X)
	default:
		return nil
	}
}

func hasNamedFields(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		if _, matches := element.(*ast.KeyValueExpr); !matches {
			return false
		}
	}

	return true
}

func literalField(literal *ast.CompositeLit, fieldName string) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		field, matches := element.(*ast.KeyValueExpr)
		if !matches {
			continue
		}
		name, matches := field.Key.(*ast.Ident)
		if matches && name.Name == fieldName {
			return field.Value, true
		}
	}

	return nil, false
}

func controllerPrefix(pkg *loadedPackage, literal *ast.CompositeLit) (string, bool) {
	value, exists := literalField(literal, "Prefix")
	if !exists {
		return "", true
	}

	return controllerConst(pkg, value)
}

func resolveEntityType(pkg *loadedPackage, function *ast.FuncDecl, expression ast.Expr, exists bool) (types.Type, bool) {
	if !exists {
		return nil, false
	}
	literal, matches := localValue(pkg, function, expression).(*ast.CompositeLit)
	if !matches || len(literal.Elts) != 0 {
		return nil, false
	}
	named, matches := types.Unalias(pkg.packageInfo.TypesInfo.TypeOf(literal)).(*types.Named)
	if !matches || named.Obj() == nil || named.Obj().Pkg() == nil {
		return nil, false
	}
	if _, matches = named.Underlying().(*types.Struct); !matches {
		return nil, false
	}

	return named, true
}

func controllerService(
	pkg *loadedPackage,
	signature *types.Signature,
	expression ast.Expr,
	exists bool,
	services []ServiceDeclaration,
) (ServiceDeclaration, types.Type, bool) {
	if !exists {
		return ServiceDeclaration{}, nil, false
	}
	identifier, matches := unparenExpr(expression).(*ast.Ident)
	if !matches {
		return ServiceDeclaration{}, nil, false
	}
	object, _ := pkg.packageInfo.TypesInfo.Uses[identifier].(*types.Var)
	if object == nil || !isControllerParam(signature, object) {
		return ServiceDeclaration{}, nil, false
	}
	pointer, matches := types.Unalias(object.Type()).(*types.Pointer)
	if !matches {
		return ServiceDeclaration{}, nil, false
	}
	serviceType, matches := types.Unalias(pointer.Elem()).(*types.Named)
	if !matches {
		return ServiceDeclaration{}, nil, false
	}
	for _, service := range services {
		if service.typ != nil && types.Identical(service.typ, serviceType) && hasPointerBase(service.typ) {
			return service, object.Type(), true
		}
	}

	return ServiceDeclaration{}, nil, false
}

func hasPointerBase(service *types.Named) bool {
	structure, matches := service.Underlying().(*types.Struct)
	if !matches {
		return false
	}
	count := 0
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Embedded() {
			continue
		}
		if _, matches := types.Unalias(field.Type()).(*types.Pointer); !matches {
			continue
		}
		if _, _, matches := baseTypeArguments(field.Type()); matches {
			count++
		}
	}

	return count == 1
}

func isControllerParam(signature *types.Signature, target *types.Var) bool {
	for index := range signature.Params().Len() {
		if signature.Params().At(index) == target {
			return true
		}
	}

	return false
}

func resolveInsertType(pkg *loadedPackage, function *ast.FuncDecl, literal *ast.CompositeLit) (types.Type, bool) {
	expression, exists := literalField(literal, "InsertParam")
	if !exists {
		return nil, true
	}
	if identifier, matches := unparenExpr(expression).(*ast.Ident); matches && identifier.Name == "nil" {
		return nil, true
	}
	call, matches := localValue(pkg, function, expression).(*ast.CallExpr)
	if !matches || len(call.Args) != 1 || !packageFunction(calledFunction(pkg.packageInfo.TypesInfo, call.Fun), gnctrlPath, "Insert") {
		return nil, false
	}
	signature, matches := types.Unalias(pkg.packageInfo.TypesInfo.TypeOf(call.Args[0])).(*types.Signature)
	if !matches || signature.Params().Len() != 2 {
		return nil, false
	}
	pointer, matches := types.Unalias(signature.Params().At(1).Type()).(*types.Pointer)
	if !matches {
		return nil, false
	}
	mutable, matches := types.Unalias(pointer.Elem()).(*types.Named)
	if !matches || mutable.Obj() == nil || mutable.Obj().Pkg() == nil ||
		mutable.Obj().Pkg().Path() != servicePackagePath || mutable.Obj().Name() != "Mutable" || mutable.TypeArgs().Len() != 1 {
		return nil, false
	}

	return mutable.TypeArgs().At(0), true
}

func unparenExpr(expression ast.Expr) ast.Expr {
	for {
		parenthesized, matches := expression.(*ast.ParenExpr)
		if !matches {
			return expression
		}
		expression = parenthesized.X
	}
}
