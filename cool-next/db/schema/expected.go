package schema

import (
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

func expectedTable(dialect driver.Dialect, metadata gnentity.Metadata) (Table, error) {
	if metadata == nil {
		return Table{}, gerror.New("实体元数据不能为 nil")
	}
	if _, err := dialect.Quote(metadata.Table()); err != nil {
		return Table{}, gerror.Wrap(err, "校验实体表名")
	}

	table := Table{Name: metadata.Table()}
	for _, field := range metadata.PersistentFields() {
		typ, err := columnType(dialect, field)
		if err != nil {
			return Table{}, gerror.Wrapf(err, "构建表 %s 的期望字段", metadata.Table())
		}
		table.Columns = append(table.Columns, Column{
			Name:          field.Column(),
			Type:          typeName(dialect.Kind(), typ),
			Nullable:      field.Nullable(),
			Primary:       field.Primary(),
			AutoIncrement: field.AutoIncrement(),
		})
	}
	for _, source := range metadata.Indexes() {
		fields := make([]string, 0, len(source.Fields))
		for _, fieldName := range source.Fields {
			field, exists := metadata.Field(fieldName)
			if !exists {
				return Table{}, gerror.Newf("索引 %s 引用未知字段 %s", source.Name, fieldName)
			}
			if !field.Persistent() {
				return Table{}, gerror.Newf("索引 %s 引用非持久化字段 %s", source.Name, fieldName)
			}
			fields = append(fields, field.Column())
		}
		table.Indexes = append(table.Indexes, Index{
			Name:   source.Name,
			Fields: fields,
			Unique: source.Unique,
		})
	}

	return table, nil
}

func columnType(dialect driver.Dialect, field gnentity.Field) (string, error) {
	if field.AutoIncrement() {
		switch dialect.Kind() {
		case driver.MySQL:
			return "BIGINT UNSIGNED", nil
		case driver.PostgreSQL:
			return "BIGSERIAL", nil
		case driver.SQLite:
			return "INTEGER", nil
		}
	}
	return dialect.ColumnType(field)
}

func typeName(kind driver.Kind, raw string) string {
	typeName := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
	switch kind {
	case driver.MySQL:
		if typeName == "TINYINT(1)" {
			return "BOOLEAN"
		}
		return typeName
	case driver.PostgreSQL:
		switch typeName {
		case "BIGSERIAL", "BIGINT", "INT8":
			return "BIGINT"
		case "INTEGER", "INT4":
			return "INTEGER"
		case "SMALLINT", "INT2":
			return "SMALLINT"
		case "BOOLEAN", "BOOL":
			return "BOOLEAN"
		case "DOUBLE PRECISION", "FLOAT8":
			return "DOUBLE PRECISION"
		case "REAL", "FLOAT4":
			return "REAL"
		case "TIMESTAMP(6) WITHOUT TIME ZONE", "TIMESTAMP":
			return "TIMESTAMP"
		case "BYTEA":
			return "BYTEA"
		}
		if strings.HasPrefix(typeName, "CHARACTER VARYING") {
			return "VARCHAR" + strings.TrimPrefix(typeName, "CHARACTER VARYING")
		}
		if strings.HasPrefix(typeName, "VARCHAR") {
			return typeName
		}
		if strings.HasPrefix(typeName, "NUMERIC") {
			return typeName
		}
		return typeName
	case driver.SQLite:
		switch {
		case strings.Contains(typeName, "INT"):
			return "INTEGER"
		case strings.Contains(typeName, "CHAR"), strings.Contains(typeName, "CLOB"), strings.Contains(typeName, "TEXT"):
			return "TEXT"
		case strings.Contains(typeName, "BLOB") || typeName == "":
			return "BLOB"
		case strings.Contains(typeName, "REAL"), strings.Contains(typeName, "FLOA"), strings.Contains(typeName, "DOUB"):
			return "REAL"
		default:
			return "NUMERIC"
		}
	default:
		return fmt.Sprintf("%s", typeName)
	}
}
