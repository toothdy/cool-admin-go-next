package driver

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 将实体元数据编译为方言 DDL
func (d Dialect) Compile(metadata gnentity.Metadata) (DDL, error) {
	if metadata == nil {
		return DDL{}, gerror.New("实体元数据不能为 nil")
	}
	table, err := d.Quote(metadata.Table())
	if err != nil {
		return DDL{}, gerror.Wrap(err, "引用实体表名")
	}
	fields := metadata.PersistentFields()
	if len(fields) == 0 {
		return DDL{}, gerror.Newf("实体表 %s 没有字段", metadata.Table())
	}

	ddl := DDL{}
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		column, err := d.compileColumn(field)
		if err != nil {
			return DDL{}, gerror.Wrapf(err, "编译表 %s 的字段", metadata.Table())
		}
		columns = append(columns, column)
	}

	ddl.CreateTable = fmt.Sprintf("CREATE TABLE %s (%s)", table, strings.Join(columns, ", "))
	switch d.kind {
	case MySQL:
		description, err := d.stringLiteral(metadata.Description())
		if err != nil {
			return DDL{}, gerror.Wrap(err, "编译 MySQL 表注释")
		}
		ddl.CreateTable += " ENGINE=InnoDB COMMENT=" + description
	case PostgreSQL:
		comments, err := d.compilePGComments(table, metadata, fields)
		if err != nil {
			return DDL{}, err
		}
		ddl.Comments = comments
	}

	ddl.Indexes, err = d.compileIndexes(table, metadata)
	if err != nil {
		return DDL{}, err
	}

	return ddl, nil
}

// 将单个实体字段编译为方言列定义
func (d Dialect) CompileColumn(field gnentity.Field) (string, error) {
	return d.compileColumn(field)
}

func (d Dialect) compileColumn(field gnentity.Field) (string, error) {
	if field == nil {
		return "", gerror.New("字段元数据不能为 nil")
	}
	column, err := d.Quote(field.Column())
	if err != nil {
		return "", gerror.Wrapf(err, "引用字段 %s", field.Name())
	}
	columnType, err := d.ColumnType(field)
	if err != nil {
		return "", err
	}
	if field.AutoIncrement() {
		if !field.Primary() || field.Nullable() ||
			field.LogicalType() != gnentity.LogicalInt && field.LogicalType() != gnentity.LogicalUint {
			return "", gerror.Newf("字段 %s 不满足自增主键约束", field.Name())
		}
		if field.Constraints().HasDefault {
			return "", gerror.Newf("自增字段 %s 不能声明默认值", field.Name())
		}
		if d.kind == MySQL {
			columnType = "BIGINT UNSIGNED"
		}
		if d.kind == PostgreSQL {
			columnType = "BIGSERIAL"
		}
		if d.kind == SQLite {
			columnType = "INTEGER"
		}
	}

	parts := []string{column, columnType}
	if !field.Nullable() {
		parts = append(parts, "NOT NULL")
	}
	if field.AutoIncrement() && d.kind == MySQL {
		parts = append(parts, "AUTO_INCREMENT")
	}
	if field.Primary() {
		parts = append(parts, "PRIMARY KEY")
	}
	if field.AutoIncrement() && d.kind == SQLite {
		parts = append(parts, "AUTOINCREMENT")
	}
	if field.Constraints().HasDefault {
		defaultValue, err := d.compileDefault(field)
		if err != nil {
			return "", err
		}
		parts = append(parts, "DEFAULT", defaultValue)
	}
	parts = append(parts, d.compileChecks(column, field)...)
	if d.kind == MySQL {
		description, err := d.stringLiteral(field.Description())
		if err != nil {
			return "", gerror.Wrapf(err, "编译字段 %s 注释", field.Name())
		}
		parts = append(parts, "COMMENT", description)
	}

	return strings.Join(parts, " "), nil
}

func (d Dialect) compileChecks(column string, field gnentity.Field) []string {
	checks := make([]string, 0, 2)
	if field.LogicalType() == gnentity.LogicalUint && d.kind != MySQL {
		checks = append(checks, fmt.Sprintf("CHECK (%s >= 0)", column))
	}
	if field.LogicalType() == gnentity.LogicalBool && d.kind == SQLite {
		checks = append(checks, fmt.Sprintf("CHECK (%s IN (0, 1))", column))
	}
	if field.LogicalType() == gnentity.LogicalJSON && d.kind == SQLite {
		checks = append(checks, fmt.Sprintf("CHECK (json_valid(%s))", column))
	}

	constraints := field.Constraints()
	if constraints.HasSize {
		switch {
		case field.LogicalType() == gnentity.LogicalString && d.kind == SQLite:
			checks = append(checks, fmt.Sprintf("CHECK (length(%s) <= %d)", column, constraints.Size))
		case field.LogicalType() == gnentity.LogicalBytes && d.kind == PostgreSQL:
			checks = append(checks, fmt.Sprintf("CHECK (octet_length(%s) <= %d)", column, constraints.Size))
		case field.LogicalType() == gnentity.LogicalBytes && d.kind == SQLite:
			checks = append(checks, fmt.Sprintf("CHECK (length(%s) <= %d)", column, constraints.Size))
		}
	}

	return checks
}

func (d Dialect) compileDefault(field gnentity.Field) (string, error) {
	raw := field.Constraints().Default
	fieldType := field.GoType()
	if fieldType == nil {
		return "", gerror.Newf("字段 %s 缺少 Go 类型", field.Name())
	}
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}

	switch field.LogicalType() {
	case gnentity.LogicalBool:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return "", gerror.Wrapf(err, "字段 %s 的布尔默认值无效", field.Name())
		}
		if d.kind == SQLite {
			if value {
				return "1", nil
			}
			return "0", nil
		}
		return strings.ToUpper(strconv.FormatBool(value)), nil
	case gnentity.LogicalInt:
		bits, err := integerBits(fieldType.Kind(), false)
		if err != nil {
			return "", err
		}
		value, err := strconv.ParseInt(raw, 10, bits)
		if err != nil {
			return "", gerror.Wrapf(err, "字段 %s 的整数默认值无效", field.Name())
		}
		return strconv.FormatInt(value, 10), nil
	case gnentity.LogicalUint:
		bits, err := integerBits(fieldType.Kind(), true)
		if err != nil {
			return "", err
		}
		value, err := strconv.ParseUint(raw, 10, bits)
		if err != nil {
			return "", gerror.Wrapf(err, "字段 %s 的无符号整数默认值无效", field.Name())
		}
		return strconv.FormatUint(value, 10), nil
	case gnentity.LogicalFloat:
		bits := 64
		if fieldType.Kind() == reflect.Float32 {
			bits = 32
		}
		value, err := strconv.ParseFloat(raw, bits)
		if err != nil {
			return "", gerror.Wrapf(err, "字段 %s 的浮点默认值无效", field.Name())
		}
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return "", gerror.Newf("字段 %s 的浮点默认值必须是有限数", field.Name())
		}
		return strconv.FormatFloat(value, 'g', -1, bits), nil
	case gnentity.LogicalString:
		if d.kind == MySQL && !field.Constraints().HasSize {
			return "", gerror.Newf(
				"MySQL 8.0.0 基线不支持字段 %s 的 TEXT 默认值",
				field.Name(),
			)
		}
		return d.stringLiteral(raw)
	case gnentity.LogicalBytes:
		return "", gerror.Newf("字段 %s 的字节默认值不可跨数据库表达", field.Name())
	case gnentity.LogicalJSON:
		return "", gerror.Newf("字段 %s 的 JSON 默认值不可跨数据库表达", field.Name())
	case gnentity.LogicalTime:
		if !strings.EqualFold(raw, "CURRENT_TIMESTAMP") {
			return "", gerror.Newf("字段 %s 的时间默认值无效", field.Name())
		}
		if d.kind == MySQL {
			return "CURRENT_TIMESTAMP(6)", nil
		}
		return "CURRENT_TIMESTAMP", nil
	default:
		return "", gerror.Newf("字段 %s 的逻辑类型无法生成默认值", field.Name())
	}
}

func (d Dialect) compilePGComments(
	table string,
	metadata gnentity.Metadata,
	fields []gnentity.Field,
) ([]string, error) {
	description, err := d.stringLiteral(metadata.Description())
	if err != nil {
		return nil, gerror.Wrap(err, "编译 PostgreSQL 表注释")
	}
	comments := []string{fmt.Sprintf("COMMENT ON TABLE %s IS %s", table, description)}
	for _, field := range fields {
		column, err := d.Quote(field.Column())
		if err != nil {
			return nil, err
		}
		description, err := d.stringLiteral(field.Description())
		if err != nil {
			return nil, gerror.Wrapf(err, "编译字段 %s 注释", field.Name())
		}
		comments = append(
			comments,
			fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s", table, column, description),
		)
	}

	return comments, nil
}

func (d Dialect) compileIndexes(table string, metadata gnentity.Metadata) ([]string, error) {
	indexes := metadata.Indexes()
	statements := make([]string, 0, len(indexes))
	for _, index := range indexes {
		name, err := d.Quote(index.Name)
		if err != nil {
			return nil, gerror.Wrapf(err, "引用索引 %s", index.Name)
		}
		if len(index.Fields) == 0 {
			return nil, gerror.Newf("索引 %s 没有字段", index.Name)
		}

		columns := make([]string, 0, len(index.Fields))
		for _, fieldName := range index.Fields {
			field, exists := metadata.Field(fieldName)
			if !exists {
				return nil, gerror.Newf("索引 %s 引用未知字段 %s", index.Name, fieldName)
			}
			column, err := d.Quote(field.Column())
			if err != nil {
				return nil, gerror.Wrapf(err, "引用索引 %s 的字段", index.Name)
			}
			columns = append(columns, column)
		}

		prefix := "CREATE INDEX"
		if index.Unique {
			prefix = "CREATE UNIQUE INDEX"
		}
		statements = append(
			statements,
			fmt.Sprintf("%s %s ON %s (%s)", prefix, name, table, strings.Join(columns, ", ")),
		)
	}

	return statements, nil
}

func (d Dialect) stringLiteral(value string) (string, error) {
	if strings.ContainsRune(value, '\x00') {
		return "", gerror.New("SQL 字符串不能包含 NUL")
	}
	if d.kind == MySQL {
		value = strings.ReplaceAll(value, `\`, `\\`)
	}

	return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
}
