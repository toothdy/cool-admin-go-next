package codegen

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	indexNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	codegenLowerCamel = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	codegenTableName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

type entityMetadata struct {
	fields  []generatedField
	indexes []generatedIndex
	table   string
}

type generatedField struct {
	column      string
	declaration string
	json        string
	persistent  bool
	position    Position
}

type generatedIndex struct {
	name     string
	position Position
}

func checkEntityDecl(declaration EntityDeclaration, schema SchemaDeclaration) (entityMetadata, []Diagnostic) {
	metadata := entityMetadata{}
	if declaration.typ == nil {
		return metadata, []Diagnostic{{Code: "CG044", Message: "实体类型信息缺失", Position: declaration.position}}
	}
	var (
		diagnostics     []Diagnostic
		hasBase         bool
		hasMeta         bool
		jsonNames       = map[string]bool{"id": true, "createTime": true, "updateTime": true}
		persistentNames = map[string]bool{"id": true, "createTime": true, "updateTime": true}
		columns         = map[string]bool{"id": true, "createTime": true, "updateTime": true}
	)
	for _, field := range declaration.fields {
		if field.embedded {
			switch {
			case isNamedType(field.typ, gPackagePath, "Meta"):
				if hasMeta {
					diagnostics = append(diagnostics, diagnostic("CG045", "实体重复嵌入 g.Meta", field.position))
					continue
				}
				hasMeta = true
				table, message := parseMetaTag(field.tag)
				if message != "" {
					diagnostics = append(diagnostics, diagnostic("CG046", message, field.position))
					continue
				}
				metadata.table = table
			case isNamedType(field.typ, entityPackagePath, "Base"):
				if hasBase {
					diagnostics = append(diagnostics, diagnostic("CG047", "实体重复嵌入 entity.Base", field.position))
					continue
				}
				hasBase = true
				if !hasBaseFields(field.typ) {
					diagnostics = append(diagnostics, diagnostic("CG048", "entity.Base 必须包含 ID、CreateTime、UpdateTime 三个固定字段", field.position))
				}
			default:
				diagnostics = append(diagnostics, diagnostic("CG049", "实体不允许其他匿名字段", field.position))
			}
			continue
		}
		generated, message := checkField(field)
		if message != "" {
			diagnostics = append(diagnostics, diagnostic("CG050", message, field.position))
			continue
		}
		if jsonNames[generated.json] {
			diagnostics = append(diagnostics, diagnostic("CG051", "实体存在重复 json 名 "+generated.json, field.position))
			continue
		}
		if generated.persistent {
			if columns[generated.column] {
				diagnostics = append(diagnostics, diagnostic("CG052", "实体存在重复列名 "+generated.column, field.position))
				continue
			}
			columns[generated.column] = true
			persistentNames[generated.json] = true
			metadata.fields = append(metadata.fields, generated)
		}
		jsonNames[generated.json] = true
	}
	if !hasMeta {
		diagnostics = append(diagnostics, diagnostic("CG053", "实体必须嵌入 g.Meta", declaration.position))
	}
	if !hasBase {
		diagnostics = append(diagnostics, diagnostic("CG054", "实体必须嵌入 entity.Base", declaration.position))
	}
	if metadata.table != "" && !codegenTableName.MatchString(metadata.table) {
		diagnostics = append(diagnostics, diagnostic("CG055", "实体表名无效", declaration.position))
	}
	if len(diagnostics) > 0 {
		return metadata, diagnostics
	}

	indexes, schemaDiagnostics := parseSchemaIndexes(schema, persistentNames, metadata.table)
	metadata.indexes = indexes
	return metadata, schemaDiagnostics
}

func parseMetaTag(tag string) (string, string) {
	value, exists := lookupTag(tag, "orm")
	if !exists || value == "" {
		return "", "g.Meta 缺少 table 指令"
	}
	var table string
	for _, directive := range strings.Split(value, ",") {
		key, value, found := strings.Cut(directive, ":")
		if !found || key != "table" || value == "" || table != "" {
			return "", "g.Meta 的 orm 指令无效"
		}
		table = value
	}
	if description, exists := lookupTag(tag, "description"); !exists || strings.TrimSpace(description) == "" {
		return "", "g.Meta 的 description 不能为空"
	}
	return table, ""
}

func hasBaseFields(value types.Type) bool {
	named, ok := types.Unalias(value).(*types.Named)
	if !ok {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	return ok && structure.NumFields() == 3 && structure.Field(0).Name() == "ID" && structure.Field(1).Name() == "CreateTime" && structure.Field(2).Name() == "UpdateTime"
}

func checkField(field entityField) (generatedField, string) {
	if field.variable == nil || !field.variable.Exported() {
		return generatedField{}, "实体不允许未导出字段"
	}
	switch field.variable.Name() {
	case "ID", "CreateTime", "UpdateTime":
		return generatedField{}, "字段名与 entity.Base 冲突"
	}
	json, exists := lookupTag(field.tag, "json")
	if !exists {
		return generatedField{}, "字段缺少 json 标签"
	}
	if json == "" || json == "-" || strings.Contains(json, ",") || !codegenLowerCamel.MatchString(json) {
		return generatedField{}, "字段 json 标签无效"
	}
	if description, exists := lookupTag(field.tag, "description"); !exists || strings.TrimSpace(description) == "" {
		return generatedField{}, "字段 description 不能为空"
	}
	markers, message := parseCoolMarkers(field.tag)
	if message != "" {
		return generatedField{}, message
	}
	column, hasColumn := lookupTag(field.tag, "orm")
	if markers.isTransient {
		if hasColumn {
			return generatedField{}, "字段 cool 标签的 transient 不能声明 orm"
		}
	} else {
		if !hasColumn {
			return generatedField{}, "字段缺少 orm 标签"
		}
		if column == "" || strings.Contains(column, ",") || !codegenLowerCamel.MatchString(column) {
			return generatedField{}, "字段 orm 列名无效"
		}
	}
	logicalType, message := codegenLogicalType(field.typ, markers.isJSON, markers.isTransient)
	if message != "" {
		return generatedField{}, message
	}
	if message = validateCoolTag(field.tag, logicalType); message != "" {
		return generatedField{}, message
	}
	return generatedField{
		column:      column,
		declaration: field.variable.Name(),
		json:        json,
		persistent:  !markers.isTransient,
		position:    field.position,
	}, ""
}

func lookupTag(tag, key string) (string, bool) {
	return reflect.StructTag(tag).Lookup(key)
}

func codegenLogicalType(value types.Type, isJSON, isTransient bool) (string, string) {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
		if _, nested := value.(*types.Pointer); nested {
			return "", "字段类型不支持双重指针"
		}
	}
	if isNamedType(value, "time", "Time") || isNamedType(value, "github.com/gogf/gf/v2/os/gtime", "Time") {
		return "time", ""
	}
	if slice, ok := value.Underlying().(*types.Slice); ok {
		if basic, ok := types.Unalias(slice.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 {
			if isJSON {
				return "", "字段 cool 标签的 json 不支持字节数组"
			}
			return "bytes", ""
		}
	}
	if isJSON {
		switch underlying := value.Underlying().(type) {
		case *types.Slice:
			return "json", ""
		case *types.Map:
			key, ok := types.Unalias(underlying.Key()).Underlying().(*types.Basic)
			if ok && key.Kind() == types.String {
				return "json", ""
			}
		}
		return "", "字段 cool 标签的 json 只支持非字节 slice 或 string key map"
	}
	if isTransient && codegenScalarSlice(value) {
		return "json", ""
	}
	basic, ok := value.Underlying().(*types.Basic)
	if !ok {
		return "", "字段类型不受支持"
	}
	switch basic.Kind() {
	case types.Bool:
		return "bool", ""
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		return "int", ""
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return "uint", ""
	case types.Float32, types.Float64:
		return "float", ""
	case types.String:
		return "string", ""
	default:
		return "", "字段类型不受支持"
	}
}

type codegenCoolMarkers struct {
	isJSON      bool
	isTransient bool
}

func parseCoolMarkers(tag string) (codegenCoolMarkers, string) {
	raw, exists := lookupTag(tag, "cool")
	if !exists {
		return codegenCoolMarkers{}, ""
	}
	if raw == "" {
		return codegenCoolMarkers{}, "字段 cool 标签不能为空"
	}
	markers := codegenCoolMarkers{}
	seen := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(item, "=")
		if key == "" || seen[key] {
			return codegenCoolMarkers{}, "字段 cool 标签无效"
		}
		seen[key] = true
		if key == "transient" {
			if found {
				return codegenCoolMarkers{}, "字段 cool 标签的 transient 无效"
			}
			markers.isTransient = true
			continue
		}
		if !found || value == "" {
			return codegenCoolMarkers{}, "字段 cool 标签无效"
		}
		if key == "json" && value == "true" {
			markers.isJSON = true
		}
	}

	return markers, ""
}

func codegenScalarSlice(value types.Type) bool {
	slice, ok := types.Unalias(value).Underlying().(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := types.Unalias(slice.Elem()).Underlying().(*types.Basic)
	if !ok {
		return false
	}
	switch basic.Kind() {
	case types.Bool,
		types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Float32, types.Float64, types.String:
		return true
	default:
		return false
	}
}

func validateCoolTag(tag, logicalType string) string {
	raw, exists := lookupTag(tag, "cool")
	if !exists {
		return ""
	}
	if raw == "" {
		return "字段 cool 标签不能为空"
	}
	seen := make(map[string]bool)
	var (
		hasPrecision bool
		hasScale     bool
		precision    uint64
		scale        uint64
	)
	for _, item := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(item, "=")
		if key == "" || seen[key] {
			return "字段 cool 标签无效"
		}
		seen[key] = true
		if key == "transient" {
			if found {
				return "字段 cool 标签的 transient 无效"
			}
			continue
		}
		if !found || value == "" {
			return "字段 cool 标签无效"
		}
		switch key {
		case "size":
			if logicalType != "string" && logicalType != "bytes" || !isPositiveUint(value) {
				return "字段 cool 标签的 size 无效"
			}
		case "default":
			if logicalType == "json" {
				return "字段 cool 标签的 default 无效"
			}
		case "json":
			if logicalType != "json" || value != "true" {
				return "字段 cool 标签的 json 无效"
			}
		case "precision":
			if logicalType != "float" || !isPositiveUint(value) {
				return "字段 cool 标签的 precision 无效"
			}
			precision, _ = strconv.ParseUint(value, 10, 64)
			hasPrecision = true
		case "scale":
			parsed, err := strconv.ParseUint(value, 10, 64)
			if logicalType != "float" || err != nil {
				return "字段 cool 标签的 scale 无效"
			}
			scale = parsed
			hasScale = true
		default:
			return "字段 cool 标签包含未知属性 " + key
		}
	}
	if hasScale && (!hasPrecision || scale > precision) {
		return "字段 cool 标签的 scale 无效"
	}
	return ""
}

func isPositiveUint(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0
}

func parseSchemaIndexes(schema SchemaDeclaration, fields map[string]bool, table string) ([]generatedIndex, []Diagnostic) {
	if schema.source == nil || schema.source.function == nil {
		return nil, []Diagnostic{{Code: "CG056", Message: "Schema 源码信息缺失", Position: schema.position}}
	}
	function := schema.source.function
	if function.Body == nil || len(function.Body.List) != 1 {
		return nil, []Diagnostic{{Code: "CG057", Message: "Schema 必须返回静态 entity.Schema 字面量", Position: schema.position}}
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return nil, []Diagnostic{{Code: "CG057", Message: "Schema 必须返回静态 entity.Schema 字面量", Position: schema.position}}
	}
	literal, ok := returned.Results[0].(*ast.CompositeLit)
	if !ok || !isNamedType(schema.source.pkg.packageInfo.TypesInfo.Types[literal].Type, entityPackagePath, "Schema") {
		return nil, []Diagnostic{{Code: "CG057", Message: "Schema 必须返回静态 entity.Schema 字面量", Position: schemaPosition(schema.source, returned.Pos())}}
	}
	seen := map[string]bool{
		"idx_" + table + "_create_time": true,
		"idx_" + table + "_update_time": true,
	}
	indexes := []generatedIndex{
		{name: "idx_" + table + "_create_time", position: schema.position},
		{name: "idx_" + table + "_update_time", position: schema.position},
	}
	var diagnostics []Diagnostic
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "CG058", Message: "Schema 必须使用 Indexes 命名字段", Position: schemaPosition(schema.source, element.Pos())})
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "Indexes" {
			diagnostics = append(diagnostics, Diagnostic{Code: "CG058", Message: "Schema 只允许声明 Indexes 字段", Position: schemaPosition(schema.source, field.Pos())})
			continue
		}
		values, ok := field.Value.(*ast.CompositeLit)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "CG059", Message: "Schema Indexes 必须是静态列表", Position: schemaPosition(schema.source, field.Value.Pos())})
			continue
		}
		for _, item := range values.Elts {
			index, message := parseIndexCall(schema.source, item, fields)
			if message != "" {
				diagnostics = append(diagnostics, Diagnostic{Code: "CG060", Message: message, Position: schemaPosition(schema.source, item.Pos())})
				continue
			}
			if !indexNamePattern.MatchString(index.name) || seen[index.name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "CG061", Message: "Schema 索引名无效或重复", Position: index.position})
				continue
			}
			seen[index.name] = true
			indexes = append(indexes, index)
		}
	}
	return indexes, diagnostics
}

func parseIndexCall(source *schemaSource, value ast.Expr, fields map[string]bool) (generatedIndex, string) {
	call, ok := value.(*ast.CallExpr)
	if !ok {
		return generatedIndex{}, "Schema 索引必须使用 entity.IndexOf 或 entity.UniqueIndexOf"
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return generatedIndex{}, "Schema 索引必须使用 entity.IndexOf 或 entity.UniqueIndexOf"
	}
	function, _ := source.pkg.packageInfo.TypesInfo.Uses[selector.Sel].(*types.Func)
	if function == nil || function.Pkg() == nil || function.Pkg().Path() != entityPackagePath || (function.Name() != "IndexOf" && function.Name() != "UniqueIndexOf") {
		return generatedIndex{}, "Schema 索引必须使用 entity.IndexOf 或 entity.UniqueIndexOf"
	}
	if len(call.Args) < 2 {
		return generatedIndex{}, "Schema 索引必须包含名称和字段"
	}
	name, ok := constantString(source, call.Args[0])
	if !ok {
		return generatedIndex{}, "Schema 索引名必须是常量字符串"
	}
	seenFields := make(map[string]bool, len(call.Args)-1)
	for _, argument := range call.Args[1:] {
		field, ok := constantString(source, argument)
		if !ok || field == "" || seenFields[field] || !fields[field] {
			return generatedIndex{}, "Schema 索引字段无效、重复或不存在"
		}
		seenFields[field] = true
	}
	return generatedIndex{name: name, position: schemaPosition(source, call.Pos())}, ""
}

func constantString(source *schemaSource, expression ast.Expr) (string, bool) {
	value := source.pkg.packageInfo.TypesInfo.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func schemaPosition(source *schemaSource, position token.Pos) Position {
	if source == nil || source.pkg == nil || source.pkg.packageInfo == nil {
		return Position{}
	}
	resolved := source.pkg.packageInfo.Fset.PositionFor(position, true)
	result := positionFromPath(source.dir, resolved.Filename)
	result.Line = resolved.Line
	result.Column = resolved.Column
	return result
}

func diagnostic(code, message string, position Position) Diagnostic {
	return Diagnostic{Code: code, Message: message, Position: position}
}
