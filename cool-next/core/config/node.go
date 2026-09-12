package config

import (
	"bytes"
	"encoding"
	"fmt"
	"io"
	"math"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
)

// 配置节点运行时类型
type valueKind uint8

const (
	valueNull    valueKind = iota // null 值
	valueScalar                   // 标量
	valueObject                   // 对象
	valueList                     // 列表
	valuePointer                  // 指针
)

// 配置中间表示节点
type configNode struct {
	kind   valueKind
	schema *configSchema
	scalar reflect.Value
	base   reflect.Value
	object map[string]*configNode
	list   []*configNode
	child  *configNode
}

// 按 schema 从 Go 值构造配置节点树
func nodeFromValue(value reflect.Value, schema *configSchema) (*configNode, error) {
	switch schema.kind {
	case schemaScalar:
		if (value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64) &&
			(math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0)) {
			return nil, fmt.Errorf("代码默认配置包含非有限浮点数")
		}
		return &configNode{kind: valueScalar, schema: schema, scalar: value}, nil
	case schemaPointer:
		if value.IsNil() {
			return &configNode{kind: valueNull, schema: schema}, nil
		}
		child, err := nodeFromValue(value.Elem(), schema.element)
		if err != nil {
			return nil, err
		}
		return &configNode{kind: valuePointer, schema: schema, child: child}, nil
	case schemaStruct:
		object := make(map[string]*configNode, len(schema.fieldOrder))
		for _, field := range schema.fieldOrder {
			child, err := nodeFromValue(value.Field(field.index), field.node)
			if err != nil {
				return nil, err
			}
			object[field.name] = child
		}
		return &configNode{kind: valueObject, schema: schema, base: cloneValue(value), object: object}, nil
	case schemaMap:
		if value.IsNil() {
			return &configNode{kind: valueNull, schema: schema}, nil
		}
		object := make(map[string]*configNode, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			child, err := nodeFromValue(iterator.Value(), schema.element)
			if err != nil {
				return nil, err
			}
			object[iterator.Key().String()] = child
		}
		return &configNode{kind: valueObject, schema: schema, object: object}, nil
	case schemaSlice:
		if value.IsNil() {
			return &configNode{kind: valueNull, schema: schema}, nil
		}
		fallthrough
	case schemaArray:
		list := make([]*configNode, value.Len())
		for index := 0; index < value.Len(); index++ {
			child, err := nodeFromValue(value.Index(index), schema.element)
			if err != nil {
				return nil, err
			}
			list[index] = child
		}
		return &configNode{kind: valueList, schema: schema, list: list}, nil
	default:
		return nil, fmt.Errorf("配置类型 %s 不受支持", schema.typ)
	}
}

// 解析主配置文件 YAML 内容为配置节点树
func parseMain(content []byte, schema *configSchema, lookupEnv LookupEnv) (*configNode, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return &configNode{kind: valueObject, schema: schema, object: make(map[string]*configNode)}, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return &configNode{kind: valueObject, schema: schema, object: make(map[string]*configNode)}, nil
		}
		return nil, preserveCause("YAML 语法错误", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("只允许一个 YAML 文档")
		}
		return nil, preserveCause("YAML 额外文档无效", err)
	}
	if len(document.Content) == 0 {
		return &configNode{kind: valueObject, schema: schema, object: make(map[string]*configNode)}, nil
	}
	if document.Content[0].ShortTag() == "!!null" && document.Content[0].Value == "" && document.Content[0].Style == 0 {
		return &configNode{kind: valueObject, schema: schema, object: make(map[string]*configNode)}, nil
	}

	return parseYAMLNode(document.Content[0], schema, "", lookupEnv)
}

// 按 schema 将 YAML 节点递归转换为配置节点
func parseYAMLNode(source *yaml.Node, schema *configSchema, path string, lookupEnv LookupEnv) (*configNode, error) {
	if source == nil {
		return nil, fmt.Errorf("配置 %s 缺少 YAML 节点", displayPath(path))
	}
	if source.Kind == yaml.AliasNode || source.Alias != nil || source.Anchor != "" {
		return nil, fmt.Errorf("配置 %s 不允许 YAML 锚点或别名", displayPath(path))
	}
	if source.ShortTag() == "!!null" {
		if schema.kind != schemaPointer && schema.kind != schemaMap && schema.kind != schemaSlice {
			return nil, fmt.Errorf("配置 %s 不允许 null", displayPath(path))
		}
		return &configNode{kind: valueNull, schema: schema}, nil
	}
	if source.Kind == yaml.ScalarNode && source.ShortTag() == "!!str" {
		name, isPlaceholder, err := envName(source.Value)
		if err != nil {
			return nil, fmt.Errorf("配置 %s 的环境变量占位符无效", displayPath(path))
		}
		if isPlaceholder {
			return envNode(schema, path, name, lookupEnv)
		}
	}
	switch schema.kind {
	case schemaPointer:
		child, err := parseYAMLNode(source, schema.element, path, lookupEnv)
		if err != nil {
			return nil, err
		}
		return &configNode{kind: valuePointer, schema: schema, child: child}, nil
	case schemaStruct:
		return parseYAMLStruct(source, schema, path, lookupEnv)
	case schemaMap:
		return parseYAMLMap(source, schema, path, lookupEnv)
	case schemaSlice, schemaArray:
		return parseYAMLList(source, schema, path, lookupEnv)
	case schemaScalar:
		return parseYAMLScalar(source, schema, path)
	default:
		return nil, fmt.Errorf("配置 %s 类型不受支持", displayPath(path))
	}
}

func parseYAMLStruct(source *yaml.Node, schema *configSchema, path string, lookupEnv LookupEnv) (*configNode, error) {
	if source.Kind != yaml.MappingNode || source.ShortTag() != "!!map" {
		return nil, fmt.Errorf("配置 %s 必须是对象", displayPath(path))
	}
	object := make(map[string]*configNode, len(source.Content)/2)
	for index := 0; index < len(source.Content); index += 2 {
		key, value, err := getYAMLPair(source, index, path)
		if err != nil {
			return nil, err
		}
		field, exists := schema.fields[key]
		if !exists {
			return nil, fmt.Errorf("配置 %s 包含未知字段", joinPath(path, key))
		}
		child, err := parseYAMLNode(value, field.node, joinPath(path, key), lookupEnv)
		if err != nil {
			return nil, err
		}
		object[key] = child
	}

	return &configNode{kind: valueObject, schema: schema, object: object}, nil
}

func parseYAMLMap(source *yaml.Node, schema *configSchema, path string, lookupEnv LookupEnv) (*configNode, error) {
	if source.Kind != yaml.MappingNode || source.ShortTag() != "!!map" {
		return nil, fmt.Errorf("配置 %s 必须是 map", displayPath(path))
	}
	object := make(map[string]*configNode, len(source.Content)/2)
	for index := 0; index < len(source.Content); index += 2 {
		key, value, err := getYAMLPair(source, index, path)
		if err != nil {
			return nil, err
		}
		child, err := parseYAMLNode(value, schema.element, joinPath(path, key), lookupEnv)
		if err != nil {
			return nil, err
		}
		object[key] = child
	}

	return &configNode{kind: valueObject, schema: schema, object: object}, nil
}

// 从 YAML MappingNode 提取 key-value 对并校验合法性
func getYAMLPair(source *yaml.Node, index int, path string) (string, *yaml.Node, error) {
	if index+1 >= len(source.Content) {
		return "", nil, fmt.Errorf("配置 %s 的 YAML 对象不完整", displayPath(path))
	}
	keyNode := source.Content[index]
	if keyNode.Kind != yaml.ScalarNode || keyNode.ShortTag() != "!!str" || keyNode.Value == "<<" ||
		keyNode.Anchor != "" || keyNode.Alias != nil {
		return "", nil, fmt.Errorf("配置 %s 的对象 key 必须是普通字符串", displayPath(path))
	}
	if _, exists := findKey(source.Content[:index], keyNode.Value); exists {
		return "", nil, fmt.Errorf("配置 %s 存在重复字段", joinPath(path, keyNode.Value))
	}

	return keyNode.Value, source.Content[index+1], nil
}

func findKey(content []*yaml.Node, key string) (int, bool) {
	for index := 0; index+1 < len(content); index += 2 {
		if content[index].Value == key {
			return index, true
		}
	}

	return 0, false
}

func parseYAMLList(source *yaml.Node, schema *configSchema, path string, lookupEnv LookupEnv) (*configNode, error) {
	if source.Kind != yaml.SequenceNode || source.ShortTag() != "!!seq" {
		return nil, fmt.Errorf("配置 %s 必须是数组", displayPath(path))
	}
	if schema.kind == schemaArray && len(source.Content) != schema.length {
		return nil, fmt.Errorf("配置 %s 的数组长度必须是 %d", displayPath(path), schema.length)
	}
	list := make([]*configNode, len(source.Content))
	for index, item := range source.Content {
		child, err := parseYAMLNode(item, schema.element, fmt.Sprintf("%s[%d]", displayPath(path), index), lookupEnv)
		if err != nil {
			return nil, err
		}
		list[index] = child
	}

	return &configNode{kind: valueList, schema: schema, list: list}, nil
}

func parseYAMLScalar(source *yaml.Node, schema *configSchema, path string) (*configNode, error) {
	if source.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("配置 %s 必须是标量", displayPath(path))
	}
	value := reflect.New(schema.typ).Elem()
	tag := source.ShortTag()
	if schema.isDuration {
		if tag != "!!str" {
			return nil, fmt.Errorf("配置 %s 必须是 duration 字符串", displayPath(path))
		}
		duration, err := time.ParseDuration(source.Value)
		if err != nil {
			return nil, preserveCause(fmt.Sprintf("配置 %s 的 duration 无效", displayPath(path)), err)
		}
		value.SetInt(int64(duration))
		return &configNode{kind: valueScalar, schema: schema, scalar: value}, nil
	}
	if schema.isText {
		if tag != "!!str" {
			return nil, fmt.Errorf("配置 %s 必须是文本标量", displayPath(path))
		}
		unmarshaler := value.Addr().Interface().(encoding.TextUnmarshaler)
		if err := unmarshaler.UnmarshalText([]byte(source.Value)); err != nil {
			return nil, preserveCause(fmt.Sprintf("配置 %s 的文本标量无效", displayPath(path)), err)
		}
		return &configNode{kind: valueScalar, schema: schema, scalar: value}, nil
	}
	switch schema.typ.Kind() {
	case reflect.String:
		if tag != "!!str" {
			return nil, fmt.Errorf("配置 %s 必须是字符串", displayPath(path))
		}
		value.SetString(source.Value)
	case reflect.Bool:
		if tag != "!!bool" {
			return nil, fmt.Errorf("配置 %s 必须是布尔值", displayPath(path))
		}
		if err := source.Decode(value.Addr().Interface()); err != nil {
			return nil, preserveCause(fmt.Sprintf("配置 %s 布尔值无效", displayPath(path)), err)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if tag != "!!int" {
			return nil, fmt.Errorf("配置 %s 必须是整数", displayPath(path))
		}
		if err := source.Decode(value.Addr().Interface()); err != nil {
			return nil, preserveCause(fmt.Sprintf("配置 %s 整数超出范围", displayPath(path)), err)
		}
	case reflect.Float32, reflect.Float64:
		if tag != "!!float" && tag != "!!int" {
			return nil, fmt.Errorf("配置 %s 必须是浮点数", displayPath(path))
		}
		if err := source.Decode(value.Addr().Interface()); err != nil {
			return nil, preserveCause(fmt.Sprintf("配置 %s 浮点数无效", displayPath(path)), err)
		}
		if math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) {
			return nil, fmt.Errorf("配置 %s 浮点数无效", displayPath(path))
		}
	default:
		return nil, fmt.Errorf("配置 %s 标量类型不受支持", displayPath(path))
	}

	return &configNode{kind: valueScalar, schema: schema, scalar: value}, nil
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}

	return parent + "." + child
}

func displayPath(path string) string {
	if path == "" {
		return "<root>"
	}

	return path
}
