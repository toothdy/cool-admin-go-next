package schema

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

func inspectTable(ctx context.Context, database gdb.DB, dialect driver.Dialect, tableName string) (Table, error) {
	tables, err := database.Tables(ctx)
	if err != nil {
		return Table{}, gerror.Wrap(err, "读取数据库表列表")
	}
	if !slices.Contains(tables, tableName) {
		return Table{}, nil
	}
	columns, err := inspectColumns(ctx, database, dialect, tableName)
	if err != nil {
		return Table{}, err
	}
	indexes, err := inspectIndexes(ctx, database, dialect, tableName)
	if err != nil {
		return Table{}, err
	}
	return Table{Name: tableName, Columns: columns, Indexes: indexes}, nil
}

func inspectColumns(ctx context.Context, database gdb.DB, dialect driver.Dialect, tableName string) ([]Column, error) {
	if dialect.Kind() == driver.PostgreSQL {
		return postgresColumns(ctx, database, tableName)
	}
	fields, err := database.TableFields(ctx, tableName)
	if err != nil {
		return nil, gerror.Wrapf(err, "读取表 %s 字段", tableName)
	}
	columns := make([]Column, 0, len(fields))
	for _, field := range fields {
		isAutoIncrement := strings.Contains(strings.ToLower(field.Extra), "auto_increment") || strings.Contains(strings.ToLower(fmt.Sprint(field.Default)), "nextval")
		if dialect.Kind() == driver.SQLite && strings.EqualFold(field.Key, "pri") && typeName(driver.SQLite, field.Type) == "INTEGER" {
			isAutoIncrement = true
		}
		columns = append(columns, Column{
			Name:          field.Name,
			Type:          typeName(dialect.Kind(), field.Type),
			Nullable:      field.Null,
			Primary:       strings.EqualFold(field.Key, "pri"),
			AutoIncrement: isAutoIncrement,
		})
	}
	return columns, nil
}

func postgresColumns(ctx context.Context, database gdb.DB, tableName string) ([]Column, error) {
	const query = `
SELECT
    attribute.attname AS columnName,
    pg_catalog.format_type(attribute.atttypid, attribute.atttypmod) AS columnType,
    NOT attribute.attnotnull AS nullable,
    EXISTS (
        SELECT 1
        FROM pg_index primaryIndex
        WHERE primaryIndex.indrelid = tableRel.oid
          AND primaryIndex.indisprimary
          AND attribute.attnum = ANY(primaryIndex.indkey)
    ) AS isPrimary,
    pg_get_expr(defaultValue.adbin, defaultValue.adrelid) AS defaultValue
FROM pg_attribute attribute
JOIN pg_class tableRel ON tableRel.oid = attribute.attrelid
JOIN pg_namespace namespace ON namespace.oid = tableRel.relnamespace
LEFT JOIN pg_attrdef defaultValue
       ON defaultValue.adrelid = tableRel.oid
      AND defaultValue.adnum = attribute.attnum
WHERE namespace.nspname = current_schema()
  AND tableRel.relname = ?
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped
ORDER BY attribute.attnum`

	rows, err := database.GetAll(ctx, query, tableName)
	if err != nil {
		return nil, gerror.Wrapf(err, "读取表 %s 的 PostgreSQL 字段", tableName)
	}
	columns := make([]Column, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, Column{
			Name:          valueOf(row, "columnName"),
			Type:          typeName(driver.PostgreSQL, valueOf(row, "columnType")),
			Nullable:      boolValueOf(row, "nullable"),
			Primary:       boolValueOf(row, "isPrimary"),
			AutoIncrement: strings.Contains(strings.ToLower(valueOf(row, "defaultValue")), "nextval"),
		})
	}
	return columns, nil
}

func inspectIndexes(ctx context.Context, database gdb.DB, dialect driver.Dialect, tableName string) ([]Index, error) {
	switch dialect.Kind() {
	case driver.MySQL:
		return mysqlIndexes(ctx, database, tableName)
	case driver.PostgreSQL:
		return postgresIndexes(ctx, database, tableName)
	case driver.SQLite:
		return sqliteIndexes(ctx, database, dialect, tableName)
	default:
		return nil, gerror.Newf("不支持的数据库类型: %s", dialect.Kind())
	}
}

func mysqlIndexes(ctx context.Context, database gdb.DB, tableName string) ([]Index, error) {
	rows, err := database.GetAll(ctx, "SELECT INDEX_NAME AS indexName, NON_UNIQUE AS nonUnique, SEQ_IN_INDEX AS sequence, COLUMN_NAME AS columnName FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX", tableName)
	if err != nil {
		return nil, gerror.Wrapf(err, "读取表 %s 索引", tableName)
	}
	return collectIndexes(rows, "indexName", "nonUnique", "columnName", true), nil
}

func postgresIndexes(ctx context.Context, database gdb.DB, tableName string) ([]Index, error) {
	rows, err := database.GetAll(ctx, "SELECT indexrel.relname AS indexName, CASE WHEN idx.indisunique THEN 0 ELSE 1 END AS nonUnique, attribute.attname AS columnName FROM pg_index idx JOIN pg_class tableRel ON tableRel.oid = idx.indrelid JOIN pg_class indexrel ON indexrel.oid = idx.indexrelid JOIN pg_namespace namespace ON namespace.oid = tableRel.relnamespace JOIN unnest(idx.indkey) WITH ORDINALITY AS keys(attributeNumber, sequence) ON TRUE JOIN pg_attribute attribute ON attribute.attrelid = tableRel.oid AND attribute.attnum = keys.attributeNumber WHERE namespace.nspname = current_schema() AND tableRel.relname = ? AND NOT idx.indisprimary ORDER BY indexrel.relname, keys.sequence", tableName)
	if err != nil {
		return nil, gerror.Wrapf(err, "读取表 %s 索引", tableName)
	}
	return collectIndexes(rows, "indexName", "nonUnique", "columnName", true), nil
}

func sqliteIndexes(ctx context.Context, database gdb.DB, dialect driver.Dialect, tableName string) ([]Index, error) {
	quotedTable, err := dialect.Quote(tableName)
	if err != nil {
		return nil, err
	}
	rows, err := database.GetAll(ctx, "PRAGMA index_list("+quotedTable+")")
	if err != nil {
		return nil, gerror.Wrapf(err, "读取表 %s 索引", tableName)
	}
	indexes := make([]Index, 0, len(rows))
	for _, row := range rows {
		name := valueOf(row, "name")
		if name == "" {
			return nil, gerror.Newf("表 %s 存在无名称索引", tableName)
		}
		quotedIndex, err := dialect.Quote(name)
		if err != nil {
			return nil, err
		}
		columns, err := database.GetAll(ctx, "PRAGMA index_info("+quotedIndex+")")
		if err != nil {
			return nil, gerror.Wrapf(err, "读取索引 %s 字段", name)
		}
		index := Index{Name: name, Unique: valueOf(row, "unique") == "1"}
		for _, column := range columns {
			index.Fields = append(index.Fields, valueOf(column, "name"))
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

func collectIndexes(rows gdb.Result, nameKey string, nonUniqueKey string, columnKey string, hasPrimary bool) []Index {
	byName := make(map[string]int)
	indexes := make([]Index, 0)
	for _, row := range rows {
		name := valueOf(row, nameKey)
		if hasPrimary && strings.EqualFold(name, "PRIMARY") {
			continue
		}
		position, exists := byName[name]
		if !exists {
			position = len(indexes)
			byName[name] = position
			indexes = append(indexes, Index{Name: name, Unique: valueOf(row, nonUniqueKey) == "0"})
		}
		indexes[position].Fields = append(indexes[position].Fields, valueOf(row, columnKey))
	}
	return indexes
}

func valueOf(row gdb.Record, name string) string {
	for key, value := range row {
		if strings.EqualFold(key, name) {
			return value.String()
		}
	}
	return ""
}

func boolValueOf(row gdb.Record, name string) bool {
	for key, value := range row {
		if strings.EqualFold(key, name) {
			return value.Bool()
		}
	}
	return false
}
