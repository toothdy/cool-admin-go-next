package codegen

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

const (
	databasePackagePath = "github.com/toothdy/cool-admin-go-next/cool-next/db"
	entityPackagePath   = "github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	gdbPackagePath      = "github.com/gogf/gf/v2/database/gdb"
	gPackagePath        = "github.com/gogf/gf/v2/frame/g"
	recyclePackagePath  = "github.com/toothdy/cool-admin-go-next/cool-next/db/gnrecycle"
	servicePackagePath  = "github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	seedPackagePath     = "github.com/toothdy/cool-admin-go-next/cool-next/seed"
	frameworkModuleKey  = ".framework"
)

func (a *analysis) analyzeEntities(root string) ([]EntityDeclaration, []SchemaDeclaration) {
	entities := make(map[string]EntityDeclaration)
	var schemas []SchemaDeclaration
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] || !isEntityFile(root, fileName) {
				continue
			}
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok.String() != "type" {
					continue
				}
				for _, spec := range general.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					object, _ := pkg.packageInfo.TypesInfo.Defs[typeSpec.Name].(*types.TypeName)
					if object != nil && object.Exported() && isEntityType(object.Type()) {
						named := types.Unalias(object.Type()).(*types.Named)
						key := entityKey(pkg.packageInfo.PkgPath, object.Name())
						entities[key] = EntityDeclaration{
							name:        object.Name(),
							position:    a.position(pkg, object.Pos()),
							packageName: pkg.packageInfo.Name,
							packagePath: pkg.packageInfo.PkgPath,
							fields:      a.entityFields(pkg, named),
							typ:         named,
						}
					}
				}
			}
		}
	}
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] || !isEntityFile(root, fileName) {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv != nil || !strings.HasSuffix(function.Name.Name, "Schema") || function.Name.Name == "Schema" {
					continue
				}
				entity := strings.TrimSuffix(function.Name.Name, "Schema")
				object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
				signature, _ := object.Type().(*types.Signature)
				key := entityKey(pkg.packageInfo.PkgPath, entity)
				if signature == nil || signature.TypeParams().Len() != 0 || signature.Params().Len() != 0 || signature.Results().Len() != 1 || !isNamedType(signature.Results().At(0).Type(), entityPackagePath, "Schema") || entities[key].name == "" {
					a.add("CG020", "Schema 声明必须与同名实体配对并返回 entity.Schema", a.position(pkg, function.Pos()))
					continue
				}
				schemas = append(schemas, SchemaDeclaration{
					entity:   entity,
					name:     function.Name.Name,
					position: a.position(pkg, function.Pos()),
					source:   &schemaSource{dir: a.dir, function: function, pkg: pkg},
				})
			}
		}
	}
	result := make([]EntityDeclaration, 0, len(entities))
	for _, entity := range entities {
		result = append(result, entity)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].packagePath != result[right].packagePath {
			return result[left].packagePath < result[right].packagePath
		}
		return result[left].name < result[right].name
	})
	sort.Slice(schemas, func(left, right int) bool {
		if schemas[left].source.pkg.packageInfo.PkgPath != schemas[right].source.pkg.packageInfo.PkgPath {
			return schemas[left].source.pkg.packageInfo.PkgPath < schemas[right].source.pkg.packageInfo.PkgPath
		}
		return schemas[left].name < schemas[right].name
	})
	return result, schemas
}

func (a *analysis) entityFields(pkg *loadedPackage, named *types.Named) []entityField {
	structure, _ := named.Underlying().(*types.Struct)
	if structure == nil {
		return nil
	}
	fields := make([]entityField, structure.NumFields())
	for index := range structure.NumFields() {
		variable := structure.Field(index)
		fields[index] = entityField{
			embedded: variable.Embedded(),
			position: a.position(pkg, variable.Pos()),
			tag:      structure.Tag(index),
			typ:      variable.Type(),
			variable: variable,
		}
	}
	return fields
}

func entityKey(packagePath, name string) string { return packagePath + ":" + name }

func (a *analysis) modulePackages(root string) []*loadedPackage {
	seen := make(map[*loadedPackage]bool)
	var result []*loadedPackage
	for file, pkg := range a.packages.byFile {
		if a.eligible[file] && strings.HasPrefix(file, root+string(filepath.Separator)) && !seen[pkg] {
			seen[pkg] = true
			result = append(result, pkg)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].packageInfo.PkgPath < result[right].packageInfo.PkgPath
	})
	return result
}

func isEntityFile(root, file string) bool {
	relative, err := filepath.Rel(root, file)
	return err == nil && strings.HasPrefix(filepath.ToSlash(relative), "entity/")
}

func isEntityType(value types.Type) bool {
	named, ok := types.Unalias(value).(*types.Named)
	if !ok {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	var hasBase, hasMeta bool
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Embedded() {
			continue
		}
		hasBase = hasBase || isNamedType(field.Type(), entityPackagePath, "Base")
		hasMeta = hasMeta || isNamedType(field.Type(), gPackagePath, "Meta")
	}
	return hasBase && hasMeta
}

func isNamedType(value types.Type, packagePath, name string) bool {
	if alias, ok := value.(*types.Alias); ok {
		object := alias.Obj()
		if object.Pkg() != nil && object.Pkg().Path() == packagePath && object.Name() == name {
			return true
		}
	}
	named, ok := types.Unalias(value).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == packagePath && named.Obj().Name() == name
}
