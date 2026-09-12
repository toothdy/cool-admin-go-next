package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"strings"
)

func checkPhysicalNames(entities []compiledEntity) []Diagnostic {
	seenTables := make(map[string]compiledEntity, len(entities))
	seenIndexes := make(map[string]compiledEntity)
	var diagnostics []Diagnostic
	for _, current := range entities {
		if previous, exists := seenTables[current.metadata.table]; exists {
			diagnostics = append(diagnostics, Diagnostic{
				Code:     "CG062",
				Message:  "实体表名与 " + previous.declaration.name + " 冲突",
				Position: current.declaration.position,
			})
		} else {
			seenTables[current.metadata.table] = current
		}
		for _, index := range current.metadata.indexes {
			if previous, exists := seenIndexes[index.name]; exists {
				diagnostics = append(diagnostics, Diagnostic{
					Code:     "CG063",
					Message:  "物理索引名与 " + previous.declaration.name + " 冲突",
					Position: index.position,
				})
				continue
			}
			seenIndexes[index.name] = current
		}
	}
	return diagnostics
}

func emitFragment(current compiledEntity, providerTypeObject types.Type) DescriptorFragment {
	declaration := current.declaration
	name := identifier(current.module, declaration.packagePath, declaration.name)
	doName := "do" + name
	providerName := "descriptor" + name
	baseProviderName := "base" + name
	qualifier := declaration.packageName
	fields := descriptorFields(current.metadata)
	return DescriptorFragment{
		baseProvider: EntityProviderCandidate{
			name:     baseProviderName,
			module:   current.module,
			position: declaration.position,
			typ:      fmt.Sprintf("*service.Base[%s.%s, uint64]", qualifier, declaration.name),
		},
		compileExpression: fmt.Sprintf(
			"entity.Compile[%s.%s, uint64](%s.%sSchema())",
			qualifier,
			declaration.name,
			qualifier,
			declaration.name,
		),
		doDeclaration:   emitDO(doName, current.metadata.table, fields),
		doName:          doName,
		entity:          declaration.name,
		entityPackage:   declaration.packagePath,
		entityQualifier: qualifier,
		entityType:      declaration.typ,
		fields:          fields,
		module:          current.module,
		provider: EntityProviderCandidate{
			name:      providerName,
			module:    current.module,
			position:  declaration.position,
			typ:       providerType(declaration),
			typObject: providerTypeObject,
		},
		table: current.metadata.table,
	}
}

func descriptorFields(metadata entityMetadata) []generatedField {
	fields := []generatedField{
		{column: "id", declaration: "ID", json: "id"},
		{column: "createTime", declaration: "CreateTime", json: "createTime"},
		{column: "updateTime", declaration: "UpdateTime", json: "updateTime"},
	}
	return append(fields, metadata.fields...)
}

func emitDO(name, table string, fields []generatedField) string {
	var source strings.Builder
	fmt.Fprintf(&source, "type %s struct {\n", name)
	fmt.Fprintf(&source, "\tg.Meta `orm:\"table:%s,do:true\"`\n", table)
	for _, field := range fields {
		fmt.Fprintf(&source, "\t%s any `orm:\"%s\"`\n", field.declaration, field.column)
	}
	source.WriteString("}\n")
	formatted, err := format.Source([]byte("package generated\n\nimport \"github.com/gogf/gf/v2/frame/g\"\n\n" + source.String()))
	if err != nil {
		return source.String()
	}
	declaration := bytes.TrimPrefix(formatted, []byte("package generated\n\nimport \"github.com/gogf/gf/v2/frame/g\"\n\n"))
	return string(declaration)
}

func identifier(values ...string) string {
	var result strings.Builder
	for _, value := range values {
		for _, character := range value {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
				result.WriteRune(character)
				continue
			}
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), "_")
}
