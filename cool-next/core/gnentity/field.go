package gnentity

import (
	"fmt"

	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

var lowerCamelPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// 解析 struct tag 构造 fieldDescriptor
func parseBusinessField(field reflect.StructField, entityType reflect.Type) (*fieldDescriptor, error) {
	jsonName, exists := field.Tag.Lookup("json")
	if !exists {
		return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 缺少 json 标签", entityType, field.Name))
	}
	if jsonName == "" || jsonName == "-" || strings.Contains(jsonName, ",") ||
		!lowerCamelPattern.MatchString(jsonName) {
		return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 的 json 标签 %q 无效", entityType, field.Name, jsonName))
	}

	description := strings.TrimSpace(field.Tag.Get("description"))
	if description == "" {
		return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 的 description 不能为空", entityType, field.Name))
	}

	markers, err := parseCoolMarkers(field.Tag, entityType, field.Name)
	if err != nil {
		return nil, err
	}
	column, hasColumn := field.Tag.Lookup("orm")
	if markers.isTransient {
		if hasColumn {
			return nil, constraintError(entityType, field.Name, "transient 字段不能声明 orm")
		}
	} else {
		if !hasColumn {
			return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 缺少 orm 标签", entityType, field.Name))
		}
		if column == "" || strings.Contains(column, ",") || !lowerCamelPattern.MatchString(column) {
			return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 列名 %q 无效", entityType, field.Name, column))
		}
	}

	logicalType, isNullable, err := inferLogicalType(field.Type, markers.isJSON, markers.isTransient)
	if err != nil {
		if markers.isJSON {
			return nil, constraintError(entityType, field.Name, "json 只支持非字节 slice 或 string key map")
		}
		return nil, exception.Core(fmt.Sprintf("实体 %s 的字段 %s 的类型 %s 不受支持", entityType, field.Name, field.Type))
	}
	constraints, err := parseConstraints(field.Tag, logicalType, entityType, field.Name)
	if err != nil {
		return nil, err
	}

	return &fieldDescriptor{
		name:         jsonName,
		jsonName:     jsonName,
		column:       column,
		description:  description,
		logicalType:  logicalType,
		goType:       field.Type,
		isNullable:   isNullable,
		isPersistent: !markers.isTransient,
		constraints:  constraints,
	}, nil
}

// 从 Go 类型推导逻辑类型和可空性
func inferLogicalType(fieldType reflect.Type, isJSON, isTransient bool) (LogicalType, bool, error) {
	original := fieldType
	isNullable := false
	if fieldType.Kind() == reflect.Pointer {
		isNullable = true
		fieldType = fieldType.Elem()
		if fieldType.Kind() == reflect.Pointer {
			return "", false, exception.Core("双重指针不受支持")
		}
	}

	if fieldType == reflect.TypeFor[time.Time]() || fieldType == reflect.TypeFor[gtime.Time]() {
		return LogicalTime, isNullable, nil
	}
	if fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Uint8 {
		if isJSON {
			return "", false, exception.Core("字节数组不能声明为 JSON")
		}
		return LogicalBytes, isNullable, nil
	}
	if isJSON {
		switch fieldType.Kind() {
		case reflect.Slice:
			return LogicalJSON, isNullable, nil
		case reflect.Map:
			if fieldType.Key().Kind() == reflect.String {
				return LogicalJSON, isNullable, nil
			}
		}
		return "", false, exception.Core("JSON 字段类型无效")
	}
	if isTransient && fieldType.Kind() == reflect.Slice && isScalarKind(fieldType.Elem().Kind()) {
		return LogicalJSON, isNullable, nil
	}

	switch fieldType.Kind() {
	case reflect.Bool:
		return LogicalBool, isNullable, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return LogicalInt, isNullable, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return LogicalUint, isNullable, nil
	case reflect.Float32, reflect.Float64:
		return LogicalFloat, isNullable, nil
	case reflect.String:
		return LogicalString, isNullable, nil
	default:
		return "", false, exception.Core(fmt.Sprintf("字段类型 %s 不受支持", original))
	}
}

type coolMarkers struct {
	isJSON      bool
	isTransient bool
}

// 解析影响字段形态的 cool 标记
func parseCoolMarkers(
	tag reflect.StructTag,
	entityType reflect.Type,
	fieldName string,
) (coolMarkers, error) {
	raw, exists := tag.Lookup("cool")
	if !exists {
		return coolMarkers{}, nil
	}
	if raw == "" {
		return coolMarkers{}, constraintError(entityType, fieldName, "标签不能为空")
	}
	markers := coolMarkers{}
	seen := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(item, "=")
		if key == "" || seen[key] {
			return coolMarkers{}, constraintError(entityType, fieldName, "约束 %q 无效", item)
		}
		seen[key] = true
		if key == "transient" {
			if found {
				return coolMarkers{}, constraintError(entityType, fieldName, "transient 标记不能包含值")
			}
			markers.isTransient = true
			continue
		}
		if !found || value == "" {
			return coolMarkers{}, constraintError(entityType, fieldName, "约束 %q 无效", item)
		}
		if key == "json" && value == "true" {
			markers.isJSON = true
		}
	}

	return markers, nil
}

// 判断是否为 transient 支持的标量切片元素
func isScalarKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	default:
		return false
	}
}

// 解析 cool 标签中的约束
func parseConstraints(
	tag reflect.StructTag,
	logicalType LogicalType,
	entityType reflect.Type,
	fieldName string,
) (Constraints, error) {
	raw, exists := tag.Lookup("cool")
	if !exists {
		return Constraints{}, nil
	}
	if raw == "" {
		return Constraints{}, constraintError(entityType, fieldName, "标签不能为空")
	}

	var constraints Constraints
	seen := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(item, "=")
		if key == "" {
			return Constraints{}, constraintError(entityType, fieldName, "约束 %q 无效", item)
		}
		if seen[key] {
			return Constraints{}, constraintError(entityType, fieldName, "约束 %q 重复", key)
		}
		seen[key] = true
		if key == "transient" {
			if found {
				return Constraints{}, constraintError(entityType, fieldName, "transient 标记不能包含值")
			}
			continue
		}
		if !found || value == "" {
			return Constraints{}, constraintError(entityType, fieldName, "约束 %q 无效", item)
		}

		switch key {
		case "size":
			value, err := parsePositive(value)
			if err != nil {
				return Constraints{}, constraintCause(err, entityType, fieldName, "size 无效")
			}
			if logicalType != LogicalString && logicalType != LogicalBytes {
				return Constraints{}, constraintError(entityType, fieldName, "size 无效")
			}
			constraints.Size = value
			constraints.HasSize = true
		case "default":
			if logicalType == LogicalJSON {
				return Constraints{}, constraintError(entityType, fieldName, "default 无效")
			}
			constraints.Default = value
			constraints.HasDefault = true
		case "json":
			if logicalType != LogicalJSON || value != "true" {
				return Constraints{}, constraintError(entityType, fieldName, "json 无效")
			}
		case "precision":
			value, err := parsePositive(value)
			if err != nil {
				return Constraints{}, constraintCause(err, entityType, fieldName, "precision 无效")
			}
			if logicalType != LogicalFloat {
				return Constraints{}, constraintError(entityType, fieldName, "precision 无效")
			}
			constraints.Precision = value
			constraints.HasPrecision = true
		case "scale":
			value, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return Constraints{}, constraintCause(err, entityType, fieldName, "scale 无效")
			}
			if logicalType != LogicalFloat {
				return Constraints{}, constraintError(entityType, fieldName, "scale 无效")
			}
			constraints.Scale = value
			constraints.HasScale = true
		default:
			return Constraints{}, constraintError(entityType, fieldName, "未知约束 %q", key)
		}
	}
	if constraints.HasScale && (!constraints.HasPrecision || constraints.Scale > constraints.Precision) {
		return Constraints{}, constraintError(entityType, fieldName, "scale 必须依赖 precision 且不能更大")
	}

	return constraints, nil
}

// 解析必须为正整数的约束值
func parsePositive(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, gerror.New("约束值必须是正整数")
	}

	return parsed, nil
}

func constraintError(
	entityType reflect.Type,
	fieldName string,
	format string,
	arguments ...any,
) error {
	detail := exception.Core(fmt.Sprintf(format, arguments...))

	return exception.Core(fmt.Sprintf("实体 %s 的字段 %s 的 cool 标签无效: %s", entityType, fieldName, detail.Error()))
}

func constraintCause(
	cause error,
	entityType reflect.Type,
	fieldName string,
	detail string,
) error {
	return exception.WrapCore(
		cause, fmt.Sprintf("实体 %s 的字段 %s 的 cool 标签无效: %s",
			entityType,
			fieldName,
			detail),
	)
}
