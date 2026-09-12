package codegen

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

var serviceActionNames = []string{"Add", "Delete", "Update", "Info", "List", "Page"}

type analyzedService struct {
	declaration ServiceDeclaration
	actions     map[string]ServiceAction
}

func (a *analysis) analyzeServices(root string) []ServiceDeclaration {
	services := make(map[string]*analyzedService)
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] || !isServiceSource(root, fileName) {
				continue
			}
			for _, node := range file.Decls {
				general, matches := node.(*ast.GenDecl)
				if !matches || general.Tok.String() != "type" {
					continue
				}
				for _, spec := range general.Specs {
					typeSpec, matches := spec.(*ast.TypeSpec)
					if !matches {
						continue
					}
					object, _ := pkg.packageInfo.TypesInfo.Defs[typeSpec.Name].(*types.TypeName)
					if object == nil || !object.Exported() {
						continue
					}
					named, _ := types.Unalias(object.Type()).(*types.Named)
					if named == nil {
						continue
					}
					structure, _ := named.Underlying().(*types.Struct)
					entityType, idType, found := embeddedBaseTypes(structure)
					if !found {
						continue
					}
					declaration := ServiceDeclaration{
						entityType:  entityType,
						idType:      idType,
						name:        object.Name(),
						packageName: pkg.packageInfo.Name,
						packagePath: pkg.packageInfo.PkgPath,
						position:    a.position(pkg, object.Pos()),
						typ:         named,
					}
					current := &analyzedService{declaration: declaration, actions: make(map[string]ServiceAction)}
					for _, action := range serviceActionNames {
						current.actions[action] = ServiceAction{name: action, mode: ServiceActionBase, position: declaration.position}
					}
					services[serviceKey(pkg.packageInfo.PkgPath, object.Name())] = current
				}
			}
		}
	}
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] || !isServiceSource(root, fileName) {
				continue
			}
			for _, node := range file.Decls {
				function, matches := node.(*ast.FuncDecl)
				if !matches || function.Recv == nil {
					continue
				}
				object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
				if object == nil {
					continue
				}
				signature, _ := object.Type().(*types.Signature)
				receiver := receiverNamedType(signature)
				if receiver == nil || receiver.Obj() == nil || receiver.Obj().Pkg() == nil {
					continue
				}
				current := services[serviceKey(receiver.Obj().Pkg().Path(), receiver.Obj().Name())]
				if current == nil {
					continue
				}
				switch function.Name.Name {
				case "ModifyBefore":
					current.declaration.hasBefore = true
				case "ModifyAfter":
					current.declaration.hasAfter = true
				default:
					action, exists := current.actions[function.Name.Name]
					if !exists || !matchesAction(current.declaration.typ, function.Name.Name, signature) {
						continue
					}
					action.mode = ServiceActionOverride
					action.position = a.position(pkg, function.Pos())
					if hasBaseDelegate(pkg, function, function.Name.Name) {
						action.mode = ServiceActionDelegate
					}
					current.actions[function.Name.Name] = action
				}
			}
		}
	}
	result := make([]ServiceDeclaration, 0, len(services))
	for _, current := range services {
		for _, action := range serviceActionNames {
			current.declaration.actions = append(current.declaration.actions, current.actions[action])
		}
		result = append(result, current.declaration)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].packagePath != result[right].packagePath {
			return result[left].packagePath < result[right].packagePath
		}

		return result[left].name < result[right].name
	})

	return result
}

func embeddedBaseTypes(structure *types.Struct) (types.Type, types.Type, bool) {
	if structure == nil {
		return nil, nil, false
	}
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Embedded() {
			continue
		}
		if entityType, idType, found := baseTypeArguments(field.Type()); found {
			return entityType, idType, true
		}
	}

	return nil, nil, false
}

func baseTypeArguments(value types.Type) (types.Type, types.Type, bool) {
	value = types.Unalias(value)
	if pointer, matches := value.(*types.Pointer); matches {
		value = types.Unalias(pointer.Elem())
	}
	named, matches := value.(*types.Named)
	if !matches || named.Obj() == nil || named.Obj().Pkg() == nil ||
		named.Obj().Pkg().Path() != servicePackagePath || named.Obj().Name() != "Base" || named.TypeArgs().Len() != 2 {
		return nil, nil, false
	}

	return named.TypeArgs().At(0), named.TypeArgs().At(1), true
}

func matchesBaseAction(serviceType *types.Named, action string, signature *types.Signature) bool {
	expected := baseActionSignature(serviceType, action)

	return sameSignature(signature, expected)
}

func matchesAction(serviceType *types.Named, action string, signature *types.Signature) bool {
	if action == "Add" || action == "Delete" || action == "Update" {
		return matchesBaseAction(serviceType, action, signature)
	}
	expected := baseActionSignature(serviceType, action)
	if expected == nil || signature == nil || signature.Variadic() || signature.TypeParams().Len() != 0 ||
		signature.Params().Len() != 2 || signature.Results().Len() != 2 ||
		!types.Identical(signature.Params().At(0).Type(), expected.Params().At(0).Type()) ||
		!types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) ||
		types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
		return false
	}
	switch action {
	case "Info":
		return types.Identical(signature.Params().At(1).Type(), expected.Params().At(1).Type())
	case "List", "Page":
		request := signature.Params().At(1).Type()
		return types.Identical(request, expected.Params().At(1).Type()) || isStructPtr(request)
	default:
		return false
	}
}

func baseActionSignature(serviceType *types.Named, action string) *types.Signature {
	if serviceType == nil {
		return nil
	}
	structure, _ := serviceType.Underlying().(*types.Struct)
	if structure == nil {
		return nil
	}
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Embedded() {
			continue
		}
		if _, _, found := baseTypeArguments(field.Type()); !found {
			continue
		}
		selection := types.NewMethodSet(field.Type()).Lookup(nil, action)
		if selection == nil {
			return nil
		}
		expected, _ := selection.Obj().Type().(*types.Signature)

		return expected
	}

	return nil
}

func isStructPtr(value types.Type) bool {
	pointer, matches := types.Unalias(value).(*types.Pointer)
	if !matches {
		return false
	}
	named, matches := types.Unalias(pointer.Elem()).(*types.Named)
	if !matches || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	_, matches = named.Underlying().(*types.Struct)

	return matches
}

func sameSignature(left, right *types.Signature) bool {
	if left == nil || right == nil || left.Variadic() != right.Variadic() ||
		left.TypeParams().Len() != right.TypeParams().Len() ||
		left.Params().Len() != right.Params().Len() || left.Results().Len() != right.Results().Len() {
		return false
	}
	for index := range left.Params().Len() {
		if !types.Identical(left.Params().At(index).Type(), right.Params().At(index).Type()) {
			return false
		}
	}
	for index := range left.Results().Len() {
		if !types.Identical(left.Results().At(index).Type(), right.Results().At(index).Type()) {
			return false
		}
	}

	return true
}

func receiverNamedType(signature *types.Signature) *types.Named {
	if signature == nil || signature.Recv() == nil {
		return nil
	}
	value := types.Unalias(signature.Recv().Type())
	if pointer, matches := value.(*types.Pointer); matches {
		value = types.Unalias(pointer.Elem())
	}
	named, _ := value.(*types.Named)

	return named
}

func hasBaseDelegate(pkg *loadedPackage, function *ast.FuncDecl, action string) bool {
	if function.Body == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		return false
	}
	receiver := function.Recv.List[0].Names[0]
	receiverObject := pkg.packageInfo.TypesInfo.Defs[receiver]
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		call, matches := node.(*ast.CallExpr)
		if !matches {
			return true
		}
		method, matches := call.Fun.(*ast.SelectorExpr)
		if !matches || method.Sel.Name != action {
			return true
		}
		base, matches := method.X.(*ast.SelectorExpr)
		if !matches || base.Sel.Name != "Base" {
			return true
		}
		identifier, matches := base.X.(*ast.Ident)
		if !matches || identifier.Name != receiver.Name || pkg.packageInfo.TypesInfo.Uses[identifier] != receiverObject {
			return true
		}
		baseSelection := pkg.packageInfo.TypesInfo.Selections[base]
		methodSelection := pkg.packageInfo.TypesInfo.Selections[method]
		if baseSelection == nil || methodSelection == nil || baseSelection.Kind() != types.FieldVal || methodSelection.Kind() != types.MethodVal {
			return true
		}
		field, matches := baseSelection.Obj().(*types.Var)
		if !matches || !field.Embedded() {
			return true
		}
		methodObject := methodSelection.Obj()
		_, _, isBase := baseTypeArguments(field.Type())
		found = isBase && methodObject.Pkg() != nil && methodObject.Pkg().Path() == servicePackagePath && methodObject.Name() == action

		return !found
	})

	return found
}

func isServiceSource(root, fileName string) bool {
	relative, err := filepath.Rel(root, fileName)
	if err != nil || relative == "." {
		return false
	}
	first := strings.Split(filepath.ToSlash(relative), "/")[0]

	return first == "service"
}

func serviceKey(packagePath, name string) string { return packagePath + ":" + name }
