package config

import (
	"encoding"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ${NAME} 格式占位符的合法命名规则
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 从字符串提取 ${NAME} 占位符中的环境变量名
func envName(value string) (name string, isPlaceholder bool, err error) {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false, nil
	}

	name = strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if !envNamePattern.MatchString(name) {
		return "", false, fmt.Errorf("环境变量占位符名称无效")
	}

	return name, true, nil
}

// 按 schema 类型将环境变量值解析为配置节点
func envNode(schema *configSchema, path, name string, lookupEnv LookupEnv) (*configNode, error) {
	if schema.kind == schemaPointer {
		child, err := envNode(schema.element, path, name, lookupEnv)
		if err != nil {
			return nil, err
		}
		return &configNode{kind: valuePointer, schema: schema, child: child}, nil
	}
	if schema.kind != schemaScalar {
		return nil, fmt.Errorf("配置 %s 的环境变量占位符只能用于叶子字段", displayPath(path))
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	value, found := lookupEnv(name)
	if !found {
		return nil, fmt.Errorf("配置 %s 引用的环境变量 %s 不存在", displayPath(path), name)
	}

	parsed, err := envScalar(schema, value)
	if err != nil {
		return nil, preserveCause(
			fmt.Sprintf("配置 %s 的环境变量 %s 类型错误", displayPath(path), name),
			err,
		)
	}
	return &configNode{kind: valueScalar, schema: schema, scalar: parsed}, nil
}

// 将环境变量字符串按 schema 类型解析为 reflect.Value
func envScalar(schema *configSchema, raw string) (reflect.Value, error) {
	value := reflect.New(schema.typ).Elem()
	if schema.isDuration {
		if raw == "" {
			return reflect.Value{}, fmt.Errorf("duration 不能为空")
		}
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return reflect.Value{}, preserveCause("duration 无效", err)
		}
		value.SetInt(int64(duration))
		return value, nil
	}
	if schema.isText {
		if raw == "" {
			return reflect.Value{}, fmt.Errorf("文本标量不能为空")
		}
		unmarshaler := value.Addr().Interface().(encoding.TextUnmarshaler)
		if err := unmarshaler.UnmarshalText([]byte(raw)); err != nil {
			return reflect.Value{}, preserveCause("文本标量无效", err)
		}
		return value, nil
	}

	switch schema.typ.Kind() {
	case reflect.String:
		value.SetString(raw)
	case reflect.Bool:
		if raw != "true" && raw != "false" {
			return reflect.Value{}, fmt.Errorf("布尔值无效")
		}
		value.SetBool(raw == "true")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if raw == "" {
			return reflect.Value{}, fmt.Errorf("整数不能为空")
		}
		parsed, err := strconv.ParseInt(raw, 10, schema.typ.Bits())
		if err != nil {
			return reflect.Value{}, preserveCause("整数无效", err)
		}
		value.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if raw == "" {
			return reflect.Value{}, fmt.Errorf("无符号整数不能为空")
		}
		parsed, err := strconv.ParseUint(raw, 10, schema.typ.Bits())
		if err != nil {
			return reflect.Value{}, preserveCause("无符号整数无效", err)
		}
		value.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		if raw == "" {
			return reflect.Value{}, fmt.Errorf("浮点数不能为空")
		}
		parsed, err := strconv.ParseFloat(raw, schema.typ.Bits())
		if err != nil {
			return reflect.Value{}, preserveCause("浮点数无效", err)
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return reflect.Value{}, fmt.Errorf("浮点数无效")
		}
		value.SetFloat(parsed)
	default:
		return reflect.Value{}, fmt.Errorf("标量类型不受支持")
	}

	return value, nil
}
