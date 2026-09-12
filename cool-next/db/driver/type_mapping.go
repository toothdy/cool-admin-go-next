package driver

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 将实体字段映射为方言类型
func (d Dialect) ColumnType(field gnentity.Field) (string, error) {
	if !d.kind.valid() {
		return "", gerror.Newf("不支持的数据库类型: %s", d.kind)
	}
	if field == nil {
		return "", gerror.New("字段元数据不能为 nil")
	}

	fieldType := field.GoType()
	if fieldType == nil {
		return "", gerror.Newf("字段 %s 缺少 Go 类型", field.Name())
	}
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}

	switch field.LogicalType() {
	case gnentity.LogicalBool:
		return d.boolType(), nil
	case gnentity.LogicalInt:
		return d.integerType(fieldType.Kind(), false)
	case gnentity.LogicalUint:
		return d.integerType(fieldType.Kind(), true)
	case gnentity.LogicalFloat:
		return d.floatType(fieldType.Kind(), field.Constraints())
	case gnentity.LogicalString:
		return d.stringType(field.Constraints())
	case gnentity.LogicalBytes:
		return d.bytesType(field.Constraints())
	case gnentity.LogicalJSON:
		return d.jsonType(fieldType)
	case gnentity.LogicalTime:
		return d.timeType(), nil
	default:
		return "", gerror.Newf("字段 %s 的逻辑类型 %q 不受支持", field.Name(), field.LogicalType())
	}
}

func (d Dialect) jsonType(fieldType reflect.Type) (string, error) {
	switch fieldType.Kind() {
	case reflect.Slice:
		if fieldType.Elem().Kind() == reflect.Uint8 {
			return "", gerror.New("JSON 字段不能使用 []byte")
		}
	case reflect.Map:
		if fieldType.Key().Kind() != reflect.String {
			return "", gerror.New("JSON map 字段必须使用 string key")
		}
	default:
		return "", gerror.Newf("JSON 字段不支持 Go %s", fieldType.Kind())
	}

	switch d.kind {
	case MySQL:
		return "JSON", nil
	case PostgreSQL:
		return "JSONB", nil
	default:
		return "TEXT", nil
	}
}

func (d Dialect) boolType() string {
	if d.kind == SQLite {
		return "INTEGER"
	}

	return "BOOLEAN"
}

func (d Dialect) integerType(kind reflect.Kind, unsigned bool) (string, error) {
	bits, err := integerBits(kind, unsigned)
	if err != nil {
		return "", err
	}
	if d.kind == SQLite {
		return "INTEGER", nil
	}
	if d.kind == PostgreSQL {
		if unsigned {
			switch bits {
			case 8:
				return "SMALLINT", nil
			case 16:
				return "INTEGER", nil
			case 32:
				return "BIGINT", nil
			case 64:
				return "NUMERIC(20,0)", nil
			}
		}
		switch bits {
		case 8, 16:
			return "SMALLINT", nil
		case 32:
			return "INTEGER", nil
		case 64:
			return "BIGINT", nil
		}
	}

	var result string
	switch bits {
	case 8:
		result = "TINYINT"
	case 16:
		result = "SMALLINT"
	case 32:
		result = "INT"
	case 64:
		result = "BIGINT"
	}
	if unsigned {
		result += " UNSIGNED"
	}

	return result, nil
}

func integerBits(kind reflect.Kind, unsigned bool) (int, error) {
	if unsigned {
		switch kind {
		case reflect.Uint:
			return strconv.IntSize, nil
		case reflect.Uint8:
			return 8, nil
		case reflect.Uint16:
			return 16, nil
		case reflect.Uint32:
			return 32, nil
		case reflect.Uint64:
			return 64, nil
		default:
			return 0, gerror.Newf("无符号整数不支持 Go %s", kind)
		}
	}

	switch kind {
	case reflect.Int:
		return strconv.IntSize, nil
	case reflect.Int8:
		return 8, nil
	case reflect.Int16:
		return 16, nil
	case reflect.Int32:
		return 32, nil
	case reflect.Int64:
		return 64, nil
	default:
		return 0, gerror.Newf("有符号整数不支持 Go %s", kind)
	}
}

func (d Dialect) floatType(kind reflect.Kind, constraints gnentity.Constraints) (string, error) {
	if kind != reflect.Float32 && kind != reflect.Float64 {
		return "", gerror.Newf("浮点数不支持 Go %s", kind)
	}
	if constraints.HasPrecision {
		if constraints.Precision == 0 || constraints.HasScale && constraints.Scale > constraints.Precision {
			return "", gerror.New("浮点精度约束无效")
		}
		if d.kind == MySQL && (constraints.Precision > 65 || constraints.Scale > 30) {
			return "", gerror.Newf(
				"MySQL DECIMAL 精度超出上限: precision=%d scale=%d",
				constraints.Precision,
				constraints.Scale,
			)
		}
		if d.kind == PostgreSQL && constraints.Precision > 1000 {
			return "", gerror.Newf("PostgreSQL NUMERIC 精度超出上限: %d", constraints.Precision)
		}

		name := "NUMERIC"
		if d.kind == MySQL {
			name = "DECIMAL"
		}
		return fmt.Sprintf("%s(%d,%d)", name, constraints.Precision, constraints.Scale), nil
	}
	if constraints.HasScale {
		return "", gerror.New("浮点 scale 必须依赖 precision")
	}

	switch d.kind {
	case MySQL:
		if kind == reflect.Float32 {
			return "FLOAT", nil
		}
		return "DOUBLE", nil
	case PostgreSQL:
		if kind == reflect.Float32 {
			return "REAL", nil
		}
		return "DOUBLE PRECISION", nil
	default:
		return "REAL", nil
	}
}

func (d Dialect) stringType(constraints gnentity.Constraints) (string, error) {
	if !constraints.HasSize {
		return "TEXT", nil
	}
	if constraints.Size == 0 {
		return "", gerror.New("字符串 size 必须大于 0")
	}
	if d.kind == SQLite {
		return "TEXT", nil
	}

	return fmt.Sprintf("VARCHAR(%d)", constraints.Size), nil
}

func (d Dialect) bytesType(constraints gnentity.Constraints) (string, error) {
	if d.kind == PostgreSQL {
		return "BYTEA", nil
	}
	if d.kind == SQLite {
		return "BLOB", nil
	}
	if !constraints.HasSize {
		return "BLOB", nil
	}
	if constraints.Size == 0 {
		return "", gerror.New("MySQL 字节字段 size 必须大于 0")
	}
	switch {
	case constraints.Size <= 65535:
		return fmt.Sprintf("VARBINARY(%d)", constraints.Size), nil
	case constraints.Size <= 16777215:
		return "MEDIUMBLOB", nil
	case constraints.Size <= 4294967295:
		return "LONGBLOB", nil
	default:
		return "", gerror.Newf("MySQL 字节字段 size 超出上限: %d", constraints.Size)
	}
}

func (d Dialect) timeType() string {
	switch d.kind {
	case MySQL:
		return "DATETIME(6)"
	case PostgreSQL:
		return "TIMESTAMP(6) WITHOUT TIME ZONE"
	default:
		return "DATETIME"
	}
}
