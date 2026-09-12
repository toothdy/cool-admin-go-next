package codegen

import (
	"fmt"
	"go/types"
	"sort"
)

// Descriptor 编译产物
type DescriptorSet struct {
	fragments []DescriptorFragment
}

// Descriptor 与私有 DO 源码片段
type DescriptorFragment struct {
	baseProvider      EntityProviderCandidate
	compileExpression string
	doDeclaration     string
	doName            string
	entity            string
	entityPackage     string
	entityQualifier   string
	fields            []generatedField
	module            string
	provider          EntityProviderCandidate
	table             string
	entityType        types.Type
}

// 返回 Base Service Provider 候选
func (f DescriptorFragment) BaseProvider() EntityProviderCandidate { return f.baseProvider }

// 返回 Descriptor 构造表达式
func (f DescriptorFragment) CompileExpression() string { return f.compileExpression }

// 返回私有 DO 声明
func (f DescriptorFragment) DODeclaration() string { return f.doDeclaration }

// 返回私有 DO 类型名称
func (f DescriptorFragment) DOName() string { return f.doName }

// 返回实体名称
func (f DescriptorFragment) Entity() string { return f.entity }

// 返回实体包路径
func (f DescriptorFragment) EntityPackage() string { return f.entityPackage }

// 返回实体包引用名
func (f DescriptorFragment) EntityQualifier() string { return f.entityQualifier }

// 返回模块身份键
func (f DescriptorFragment) Module() string { return f.module }

// 返回 Descriptor Provider 候选
func (f DescriptorFragment) Provider() EntityProviderCandidate { return f.provider }

// 返回物理表名
func (f DescriptorFragment) Table() string { return f.table }

// 实体 Descriptor 的后续注册候选
type EntityProviderCandidate struct {
	name      string
	module    string
	position  Position
	typ       string
	typObject types.Type
}

// 返回候选名称
func (c EntityProviderCandidate) Name() string { return c.name }

// 返回模块身份键
func (c EntityProviderCandidate) Module() string { return c.module }

// 返回 Provider 类型文本
func (c EntityProviderCandidate) Type() string { return c.typ }

// 将分析模型编译为静态 Descriptor 和 DO 片段
func CompileDescriptors(model *Model) (*DescriptorSet, error) {
	if model == nil {
		return nil, &DiagnosticError{diagnostics: []Diagnostic{{Code: "CG040", Message: "分析模型不能为空"}}}
	}

	var (
		diagnostics []Diagnostic
		compiled    []compiledEntity
	)
	for _, current := range model.modules {
		schemas := make(map[string][]SchemaDeclaration, len(current.schemas))
		for _, schema := range current.schemas {
			if schema.source == nil || schema.source.pkg == nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "CG041", Message: "Schema 源码信息缺失", Position: schema.position})
				continue
			}
			key := entityKey(schema.source.pkg.packageInfo.PkgPath, schema.entity)
			schemas[key] = append(schemas[key], schema)
		}
		for _, declaration := range current.entities {
			key := entityKey(declaration.packagePath, declaration.name)
			matches := schemas[key]
			if len(matches) == 0 {
				diagnostics = append(diagnostics, Diagnostic{Code: "CG042", Message: "实体缺少同目录同名 Schema 声明", Position: declaration.position})
				continue
			}
			if len(matches) > 1 {
				diagnostics = append(diagnostics, Diagnostic{Code: "CG043", Message: "实体存在重复 Schema 声明", Position: matches[1].position})
				continue
			}
			metadata, entityDiagnostics := checkEntityDecl(declaration, matches[0])
			diagnostics = append(diagnostics, entityDiagnostics...)
			if len(entityDiagnostics) == 0 {
				compiled = append(compiled, compiledEntity{declaration: declaration, metadata: metadata, module: current.identity.Key()})
			}
		}
	}
	if len(diagnostics) > 0 {
		sortDiagnostics(diagnostics)
		return nil, &DiagnosticError{diagnostics: diagnostics}
	}

	sort.Slice(compiled, func(left, right int) bool {
		first, second := compiled[left], compiled[right]
		if first.module != second.module {
			return first.module < second.module
		}
		if first.declaration.packagePath != second.declaration.packagePath {
			return first.declaration.packagePath < second.declaration.packagePath
		}
		return first.declaration.name < second.declaration.name
	})
	if diagnostics = checkPhysicalNames(compiled); len(diagnostics) > 0 {
		sortDiagnostics(diagnostics)
		return nil, &DiagnosticError{diagnostics: diagnostics}
	}

	fragments := make([]DescriptorFragment, len(compiled))
	for index, current := range compiled {
		providerType, err := resolveProvider(current.declaration)
		if err != nil {
			return nil, &DiagnosticError{diagnostics: []Diagnostic{{Code: "CG064", Message: err.Error(), Position: current.declaration.position}}}
		}
		fragments[index] = emitFragment(current, providerType)
	}
	return &DescriptorSet{fragments: fragments}, nil
}

// 返回 Descriptor 片段副本
func (s *DescriptorSet) Fragments() []DescriptorFragment {
	if s == nil {
		return nil
	}
	fragments := append([]DescriptorFragment(nil), s.fragments...)
	for index := range fragments {
		fragments[index].fields = append([]generatedField(nil), fragments[index].fields...)
	}
	return fragments
}

type compiledEntity struct {
	declaration EntityDeclaration
	metadata    entityMetadata
	module      string
}

func providerType(declaration EntityDeclaration) string {
	return fmt.Sprintf("entity.Descriptor[%s.%s, uint64]", declaration.packageName, declaration.name)
}

func resolveProvider(declaration EntityDeclaration) (types.Type, error) {
	if declaration.typ == nil || declaration.typ.Obj() == nil || declaration.typ.Obj().Pkg() == nil {
		return nil, fmt.Errorf("实体 Descriptor Provider 类型信息缺失")
	}
	var entityPackage *types.Package
	for _, imported := range declaration.typ.Obj().Pkg().Imports() {
		if imported.Path() == entityPackagePath {
			entityPackage = imported
			break
		}
	}
	if entityPackage == nil {
		return nil, fmt.Errorf("无法加载 entity.Descriptor 类型")
	}
	descriptor := entityPackage.Scope().Lookup("Descriptor")
	if descriptor == nil {
		return nil, fmt.Errorf("无法加载 entity.Descriptor 类型")
	}
	instantiated, err := types.Instantiate(nil, descriptor.Type(), []types.Type{declaration.typ, types.Typ[types.Uint64]}, true)
	if err != nil {
		return nil, fmt.Errorf("构造实体 Descriptor Provider 类型失败: %w", err)
	}
	return instantiated, nil
}
