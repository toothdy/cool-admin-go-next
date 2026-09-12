package gnentity

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"

	// 表名校验正则
	"fmt"
)

var tableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// 从实体类型构造只读 Descriptor
func Compile[E any, ID comparable](schema Schema) (Descriptor[E, ID], error) {
	entityType := reflect.TypeFor[E]()
	idType := reflect.TypeFor[ID]()
	if entityType.Kind() != reflect.Struct {
		return nil, exception.Core(fmt.Sprintf("实体类型 %s 必须是非指针结构体", entityType))
	}
	if idType != reflect.TypeFor[uint64]() {
		return nil, exception.Core(fmt.Sprintf("实体 %s 的 ID 类型必须是 uint64", entityType))
	}

	var (
		table          string
		description    string
		hasMeta        bool
		hasBase        bool
		businessFields []reflect.StructField
	)
	for index := 0; index < entityType.NumField(); index++ {
		field := entityType.Field(index)
		if field.Anonymous {
			switch field.Type {
			case reflect.TypeFor[g.Meta]():
				if hasMeta {
					return nil, exception.Core(fmt.Sprintf("实体 %s 重复嵌入 g.Meta", entityType))
				}
				var err error
				table, description, err = parseMeta(field, entityType)
				if err != nil {
					return nil, err
				}
				hasMeta = true
			case reflect.TypeFor[Base]():
				if hasBase {
					return nil, exception.Core(fmt.Sprintf("实体 %s 重复嵌入 Base", entityType))
				}
				hasBase = true
			default:
				return nil, exception.Core(fmt.Sprintf("实体 %s 不允许匿名字段 %s", entityType, field.Name))
			}
			continue
		}
		if !field.IsExported() {
			return nil, exception.Core(fmt.Sprintf("实体 %s 不允许未导出字段 %s", entityType, field.Name))
		}
		businessFields = append(businessFields, field)
	}
	if !hasMeta {
		return nil, exception.Core(fmt.Sprintf("实体 %s 必须嵌入 g.Meta", entityType))
	}
	if !hasBase {
		return nil, exception.Core(fmt.Sprintf("实体 %s 必须嵌入 Base", entityType))
	}
	fields := baseFields()
	descriptor := &descriptorValue[E, ID]{
		table:            table,
		description:      description,
		entityType:       entityType,
		idType:           idType,
		primary:          fields[0],
		fields:           fields,
		persistentFields: append([]Field(nil), fields...),
		byName:           make(map[string]Field, len(fields)),
		byJSON:           make(map[string]Field, len(fields)),
		byColumn:         make(map[string]Field, len(fields)),
	}
	persistentByName := make(map[string]Field, len(fields))
	for _, field := range fields {
		descriptor.byName[field.Name()] = field
		descriptor.byJSON[field.JSONName()] = field
		descriptor.byColumn[field.Column()] = field
		persistentByName[field.Name()] = field
	}
	for _, source := range businessFields {
		field, err := parseBusinessField(source, entityType)
		if err != nil {
			return nil, err
		}
		if _, exists := descriptor.byJSON[field.JSONName()]; exists {
			return nil, exception.Core(fmt.Sprintf("实体 %s 存在重复 json 名 %s", entityType, field.JSONName()))
		}
		if _, exists := descriptor.byName[field.Name()]; exists {
			return nil, exception.Core(fmt.Sprintf("实体 %s 存在重复字段名 %s", entityType, field.Name()))
		}
		if field.Persistent() {
			if _, exists := descriptor.byColumn[field.Column()]; exists {
				return nil, exception.Core(fmt.Sprintf("实体 %s 存在重复列名 %s", entityType, field.Column()))
			}
		}
		descriptor.fields = append(descriptor.fields, field)
		descriptor.byName[field.Name()] = field
		descriptor.byJSON[field.JSONName()] = field
		if field.Persistent() {
			descriptor.persistentFields = append(descriptor.persistentFields, field)
			descriptor.byColumn[field.Column()] = field
			persistentByName[field.Name()] = field
		}
	}
	indexes, err := compileIndexes(table, schema, persistentByName)
	if err != nil {
		return nil, err
	}
	descriptor.indexes = indexes
	descriptor.doShape = compileDOShape(entityType, table, descriptor.persistentFields)

	return descriptor, nil
}

// 解析 g.Meta 的 orm 和 description 标签
func parseMeta(field reflect.StructField, entityType reflect.Type) (string, string, error) {
	orm, exists := field.Tag.Lookup("orm")
	if !exists || orm == "" {
		return "", "", exception.Core(fmt.Sprintf("实体 %s 的 g.Meta 缺少 table 指令", entityType))
	}
	var table string
	for _, directive := range strings.Split(orm, ",") {
		key, value, found := strings.Cut(directive, ":")
		if !found || key != "table" || value == "" {
			return "", "", exception.Core(fmt.Sprintf("实体 %s 的 g.Meta orm 指令 %q 无效", entityType, directive))
		}
		if table != "" {
			return "", "", exception.Core(fmt.Sprintf("实体 %s 的 g.Meta 重复声明 table", entityType))
		}
		table = value
	}
	if !tableNamePattern.MatchString(table) {
		return "", "", exception.Core(fmt.Sprintf("实体 %s 的表名 %q 无效", entityType, table))
	}
	description := strings.TrimSpace(field.Tag.Get("description"))
	if description == "" {
		return "", "", exception.Core(fmt.Sprintf("实体 %s 的表描述不能为空", entityType))
	}

	return table, description, nil
}

// 构造 Base 内嵌的固定字段
func baseFields() []Field {
	return []Field{
		&fieldDescriptor{
			name:            "id",
			jsonName:        "id",
			column:          "id",
			description:     "ID",
			logicalType:     LogicalUint,
			goType:          reflect.TypeFor[uint64](),
			isPrimary:       true,
			isAutoIncrement: true,
			isPersistent:    true,
		},
		&fieldDescriptor{
			name:               "createTime",
			jsonName:           "createTime",
			column:             "createTime",
			description:        "创建时间",
			logicalType:        LogicalTime,
			goType:             reflect.TypeFor[*gtime.Time](),
			isSystemMaintained: true,
			isPersistent:       true,
		},
		&fieldDescriptor{
			name:               "updateTime",
			jsonName:           "updateTime",
			column:             "updateTime",
			description:        "更新时间",
			logicalType:        LogicalTime,
			goType:             reflect.TypeFor[*gtime.Time](),
			isSystemMaintained: true,
			isPersistent:       true,
		},
	}
}
