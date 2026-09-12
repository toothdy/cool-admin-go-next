package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// 配置 schema 类型枚举
type schemaKind uint8

const (
	schemaScalar  schemaKind = iota // 标量
	schemaStruct                    // 结构体
	schemaMap                       // map
	schemaSlice                     // slice
	schemaArray                     // 定长数组
	schemaPointer                   // 指针
)

// 用于判断是否 duration / text 标量的反射类型缓存
var (
	durationType      = reflect.TypeOf(time.Duration(0))
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// 类型元信息，描述 Go 结构体每个字段的嵌套类型链
type configSchema struct {
	typ        reflect.Type
	kind       schemaKind
	fields     map[string]*schemaField
	fieldOrder []*schemaField
	element    *configSchema
	length     int
	isDuration bool
	isText     bool
}

// 结构体字段的 schema 条目
type schemaField struct {
	name            string
	goName          string
	validationAlias string
	index           int
	node            *configSchema
}

// 从根结构体类型构建完整 schema
func buildSchema(root reflect.Type) (*configSchema, error) {
	if root == nil || root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("配置根类型必须是非指针结构体")
	}

	return buildSchemaNode(root, make(map[reflect.Type]bool))
}

// 递归构建单个类型的 schema 节点，检测递归引用
func buildSchemaNode(current reflect.Type, stack map[reflect.Type]bool) (*configSchema, error) {
	if current == durationType {
		return &configSchema{typ: current, kind: schemaScalar, isDuration: true}, nil
	}
	if isTextScalar(current) {
		return &configSchema{typ: current, kind: schemaScalar, isText: true}, nil
	}
	if stack[current] {
		return nil, fmt.Errorf("配置类型 %s 存在递归引用", current)
	}

	node := &configSchema{typ: current}
	switch current.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		node.kind = schemaScalar
		return node, nil
	case reflect.Pointer:
		stack[current] = true
		element, err := buildSchemaNode(current.Elem(), stack)
		delete(stack, current)
		if err != nil {
			return nil, err
		}
		node.kind = schemaPointer
		node.element = element
		return node, nil
	case reflect.Map:
		if current.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("配置 map %s 的 key 必须是 string", current)
		}
		stack[current] = true
		element, err := buildSchemaNode(current.Elem(), stack)
		delete(stack, current)
		if err != nil {
			return nil, err
		}
		node.kind = schemaMap
		node.element = element
		return node, nil
	case reflect.Slice:
		stack[current] = true
		element, err := buildSchemaNode(current.Elem(), stack)
		delete(stack, current)
		if err != nil {
			return nil, err
		}
		node.kind = schemaSlice
		node.element = element
		return node, nil
	case reflect.Array:
		stack[current] = true
		element, err := buildSchemaNode(current.Elem(), stack)
		delete(stack, current)
		if err != nil {
			return nil, err
		}
		node.kind = schemaArray
		node.element = element
		node.length = current.Len()
		return node, nil
	case reflect.Struct:
		return buildStructSchema(current, stack)
	default:
		return nil, fmt.Errorf("配置类型 %s 不受支持", current)
	}
}

func buildStructSchema(current reflect.Type, stack map[reflect.Type]bool) (*configSchema, error) {
	node := &configSchema{
		typ:    current,
		kind:   schemaStruct,
		fields: make(map[string]*schemaField),
	}
	stack[current] = true
	defer delete(stack, current)

	for index := 0; index < current.NumField(); index++ {
		field := current.Field(index)
		if field.Anonymous {
			return nil, fmt.Errorf("配置结构体 %s 不允许匿名字段 %s", current, field.Name)
		}
		if !field.IsExported() {
			if hasMutableRef(field.Type, make(map[reflect.Type]bool)) {
				return nil, fmt.Errorf("配置结构体 %s 的未导出字段 %s 包含可变引用", current, field.Name)
			}
			continue
		}

		name, isIgnored, err := jsonField(field)
		if err != nil {
			return nil, err
		}
		if isIgnored {
			continue
		}
		if _, exists := node.fields[name]; exists {
			return nil, fmt.Errorf("配置结构体 %s 存在重复字段名 %s", current, name)
		}

		fieldSchema, err := buildSchemaNode(field.Type, stack)
		if err != nil {
			return nil, fmt.Errorf("配置字段 %s.%s: %w", current, name, err)
		}
		schemaField := &schemaField{
			name:            name,
			goName:          field.Name,
			validationAlias: validationAlias(field),
			index:           index,
			node:            fieldSchema,
		}
		node.fields[name] = schemaField
		node.fieldOrder = append(node.fieldOrder, schemaField)
	}

	return node, nil
}

func hasMutableRef(current reflect.Type, stack map[reflect.Type]bool) bool {
	if stack[current] {
		return false
	}
	stack[current] = true
	defer delete(stack, current)

	switch current.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface,
		reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return hasMutableRef(current.Elem(), stack)
	case reflect.Struct:
		for index := 0; index < current.NumField(); index++ {
			if hasMutableRef(current.Field(index).Type, stack) {
				return true
			}
		}
	}

	return false
}

// 将校验错误中的字段路径映射为配置 JSON 字段路径
func fieldPath(schema *configSchema, field string) string {
	parts := strings.Split(field, ".")
	path := make([]string, 0, len(parts))
	current := schema
	for _, part := range parts {
		for current != nil && current.kind == schemaPointer {
			current = current.element
		}
		if current == nil || current.kind != schemaStruct {
			path = append(path, part)
			continue
		}

		var matched *schemaField
		for _, candidate := range current.fieldOrder {
			if candidate.validationAlias == part || candidate.goName == part || candidate.name == part {
				matched = candidate
				break
			}
		}
		if matched == nil {
			path = append(path, part)
			continue
		}
		path = append(path, matched.name)
		current = matched.node
	}

	return strings.Join(path, ".")
}

// 从 struct tag 提取校验别名
func validationAlias(field reflect.StructField) string {
	rules := strings.SplitN(field.Tag.Get("v"), "#", 2)[0]
	alias, _, exists := strings.Cut(rules, "@")
	if !exists {
		return ""
	}

	return alias
}

// 从 struct tag 提取 JSON 字段名及选项
func jsonField(field reflect.StructField) (name string, isIgnored bool, err error) {
	tag, hasTag := field.Tag.Lookup("json")
	if !hasTag {
		return field.Name, false, nil
	}

	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", true, nil
	}
	name = parts[0]
	if name == "" {
		name = field.Name
	}

	seenOmitEmpty := false
	for _, option := range parts[1:] {
		if option != "omitempty" || seenOmitEmpty {
			return "", false, fmt.Errorf("配置字段 %s 的 json 选项 %q 不受支持", field.Name, option)
		}
		seenOmitEmpty = true
	}

	return name, false, nil
}

// 判断类型是否为 text 标量（实现 TextMarshaler + TextUnmarshaler 的基础类型）
func isTextScalar(current reflect.Type) bool {
	if !isSafeTextKind(current.Kind()) {
		return false
	}

	return current.Implements(textMarshalerType) && reflect.PointerTo(current).Implements(textUnmarshalType)
}

func isSafeTextKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}
