package gnentity

import (
	"fmt"

	"regexp"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

var indexNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// 普通或唯一索引
type Index struct {
	Name   string   // 索引名
	Fields []string // 索引字段逻辑名
	Unique bool     // 是否唯一索引
}

// 实体类型无法表达的补充索引
type Schema struct {
	Indexes []Index // 补充索引列表
}

// 创建普通索引声明
func IndexOf(name string, fields ...string) Index {
	return Index{Name: name, Fields: append([]string(nil), fields...)}
}

// 创建唯一索引声明
func UniqueIndexOf(name string, fields ...string) Index {
	index := IndexOf(name, fields...)
	index.Unique = true

	return index
}

// 合并系统索引和 Schema 声明的索引并校验
func compileIndexes(table string, schema Schema, fields map[string]Field) ([]Index, error) {
	indexes := []Index{
		IndexOf("idx_"+table+"_create_time", "createTime"),
		IndexOf("idx_"+table+"_update_time", "updateTime"),
	}
	seenNames := map[string]bool{
		indexes[0].Name: true,
		indexes[1].Name: true,
	}
	for _, source := range schema.Indexes {
		if !indexNamePattern.MatchString(source.Name) {
			return nil, exception.Core(fmt.Sprintf("实体表 %s 的索引名 %q 无效", table, source.Name))
		}
		if seenNames[source.Name] {
			return nil, exception.Core(fmt.Sprintf("实体表 %s 存在重复索引名 %s", table, source.Name))
		}
		if len(source.Fields) == 0 {
			return nil, exception.Core(fmt.Sprintf("实体表 %s 的索引 %s 必须包含字段", table, source.Name))
		}

		seenFields := make(map[string]bool, len(source.Fields))
		for _, field := range source.Fields {
			if field == "" {
				return nil, exception.Core(fmt.Sprintf("实体表 %s 的索引 %s 包含空字段", table, source.Name))
			}
			if seenFields[field] {
				return nil, exception.Core(fmt.Sprintf("实体表 %s 的索引 %s 包含重复字段 %s", table, source.Name, field))
			}
			if _, exists := fields[field]; !exists {
				return nil, exception.Core(fmt.Sprintf("实体表 %s 的索引 %s 引用未知字段 %s", table, source.Name, field))
			}
			seenFields[field] = true
		}

		seenNames[source.Name] = true
		indexes = append(indexes, Index{
			Name:   source.Name,
			Fields: append([]string(nil), source.Fields...),
			Unique: source.Unique,
		})
	}

	return indexes, nil
}
