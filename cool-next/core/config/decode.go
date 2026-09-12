package config

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"
)

// 从配置节点解码为 Go 值
func decodeNode(node *configNode) (reflect.Value, error) {
	if node == nil {
		return reflect.Value{}, fmt.Errorf("配置节点为空")
	}
	if node.kind == valueNull {
		return reflect.Zero(node.schema.typ), nil
	}

	schema := node.schema
	switch schema.kind {
	case schemaScalar:
		return node.scalar, nil
	case schemaPointer:
		child, err := decodeNode(node.child)
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.New(schema.typ.Elem())
		value.Elem().Set(child)
		return value, nil
	case schemaStruct:
		value := reflect.New(schema.typ).Elem()
		if node.base.IsValid() {
			value = cloneValue(node.base)
		}
		for _, field := range schema.fieldOrder {
			child, exists := node.object[field.name]
			if !exists {
				continue
			}
			decoded, err := decodeNode(child)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Field(field.index).Set(decoded)
		}
		return value, nil
	case schemaMap:
		value := reflect.MakeMapWithSize(schema.typ, len(node.object))
		for key, child := range node.object {
			decoded, err := decodeNode(child)
			if err != nil {
				return reflect.Value{}, err
			}
			mapKey := reflect.New(schema.typ.Key()).Elem()
			mapKey.SetString(key)
			value.SetMapIndex(mapKey, decoded)
		}
		return value, nil
	case schemaSlice:
		value := reflect.MakeSlice(schema.typ, len(node.list), len(node.list))
		for index, child := range node.list {
			decoded, err := decodeNode(child)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Index(index).Set(decoded)
		}
		return value, nil
	case schemaArray:
		value := reflect.New(schema.typ).Elem()
		for index, child := range node.list {
			decoded, err := decodeNode(child)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Index(index).Set(decoded)
		}
		return value, nil
	default:
		return reflect.Value{}, fmt.Errorf("配置类型 %s 无法解码", schema.typ)
	}
}

// 将配置节点序列化为按字段名排序的 JSON
func encodeCanonical(node *configNode) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	if err := writeCanonical(buffer, node); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func writeCanonical(buffer *bytes.Buffer, node *configNode) error {
	if node == nil || node.kind == valueNull {
		buffer.WriteString("null")
		return nil
	}

	switch node.kind {
	case valuePointer:
		return writeCanonical(buffer, node.child)
	case valueObject:
		return writeObject(buffer, node)
	case valueList:
		buffer.WriteByte('[')
		for index, child := range node.list {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonical(buffer, child); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
		return nil
	case valueScalar:
		return writeScalar(buffer, node)
	default:
		return fmt.Errorf("配置节点类型无法规范化")
	}
}

func writeObject(buffer *bytes.Buffer, node *configNode) error {
	keys := make([]string, 0, len(node.object))
	for key := range node.object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	buffer.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return fmt.Errorf("配置字段名无法编码")
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		if err := writeCanonical(buffer, node.object[key]); err != nil {
			return err
		}
	}
	buffer.WriteByte('}')

	return nil
}

func writeScalar(buffer *bytes.Buffer, node *configNode) error {
	value := node.scalar
	if node.schema.isDuration {
		return writeString(buffer, time.Duration(value.Int()).String())
	}
	if node.schema.isText {
		marshaler := value.Interface().(encoding.TextMarshaler)
		text, err := marshaler.MarshalText()
		if err != nil {
			return preserveCause("文本标量无法规范化", err)
		}
		return writeString(buffer, string(text))
	}

	switch value.Kind() {
	case reflect.String:
		return writeString(buffer, value.String())
	case reflect.Bool:
		buffer.WriteString(strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buffer.WriteString(strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buffer.WriteString(strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		buffer.WriteString(strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits()))
	default:
		return fmt.Errorf("标量类型 %s 无法规范化", value.Type())
	}

	return nil
}

func writeString(buffer *bytes.Buffer, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("字符串无法规范化")
	}
	buffer.Write(encoded)

	return nil
}
