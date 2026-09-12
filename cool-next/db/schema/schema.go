package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

// Descriptor 驱动的 Schema 管理器
type Manager struct {
	database gdb.DB
	dialect  driver.Dialect
}

// 创建 Schema 管理器
func New(database gdb.DB, dialect driver.Dialect) (*Manager, error) {
	if database == nil {
		return nil, gerror.New("数据库对象不能为 nil")
	}
	if _, err := dialect.Quote("cool_schema_probe"); err != nil {
		return nil, gerror.Wrap(err, "校验数据库方言")
	}
	return &Manager{database: database, dialect: dialect}, nil
}

// 按模式处理业务实体结构
func (m *Manager) Apply(ctx context.Context, mode Mode, metadata ...gnentity.Metadata) (Report, error) {
	if m == nil || m.database == nil {
		return Report{}, gerror.New("Schema 管理器未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !validMode(mode) {
		return Report{}, gerror.Newf("Schema 模式无效: %q", mode)
	}
	report := Report{Dialect: m.dialect.Kind()}
	if mode == Off {
		return report, nil
	}
	for _, descriptor := range metadata {
		expected, err := expectedTable(m.dialect, descriptor)
		if err != nil {
			return report, err
		}
		actual, err := inspectTable(ctx, m.database, m.dialect, expected.Name)
		if err != nil {
			return report, err
		}
		differences := compare(expected, actual)
		if mode == Sync && len(differences) > 0 {
			if err = m.sync(ctx, descriptor, expected, actual, differences); err != nil {
				return report, err
			}
			actual, err = inspectTable(ctx, m.database, m.dialect, expected.Name)
			if err != nil {
				return report, err
			}
			differences = compare(expected, actual)
		}
		report.Differences = append(report.Differences, differences...)
	}
	if len(report.Differences) > 0 {
		return report, &ValidationError{Report: report}
	}
	return report, nil
}

func (m *Manager) sync(ctx context.Context, metadata gnentity.Metadata, expected Table, actual Table, differences []Difference) error {
	if hasUnsafeDiff(metadata, differences) {
		return &ValidationError{Report: Report{Dialect: m.dialect.Kind(), Differences: differences}}
	}
	if actual.Name == "" {
		ddl, err := m.dialect.Compile(metadata)
		if err != nil {
			return gerror.Wrapf(err, "编译表 %s 建表语句", expected.Name)
		}
		for _, statement := range ddl.Statements() {
			if _, err = m.database.Exec(ctx, statement); err != nil {
				return gerror.Wrapf(err, "同步创建表 %s", expected.Name)
			}
		}
		return nil
	}
	for _, difference := range differences {
		if difference.Kind == "column" && difference.Actual == "缺失" {
			field, exists := metadata.Column(difference.Subject)
			if !exists {
				return gerror.Newf("表 %s 的缺失列 %s 未在 Descriptor 中找到", expected.Name, difference.Subject)
			}
			definition, err := m.dialect.CompileColumn(field)
			if err != nil {
				return gerror.Wrapf(err, "编译表 %s 的新增列 %s", expected.Name, difference.Subject)
			}
			table, err := m.dialect.Quote(expected.Name)
			if err != nil {
				return err
			}
			if _, err = m.database.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN "+definition); err != nil {
				return gerror.Wrapf(err, "同步表 %s 的新增列 %s", expected.Name, difference.Subject)
			}
		}
		if difference.Kind == "index" && difference.Actual == "缺失" {
			if err := m.createIndex(ctx, metadata, difference.Subject); err != nil {
				return err
			}
		}
	}
	if err := m.database.GetCore().ClearTableFields(ctx, expected.Name); err != nil {
		return gerror.Wrapf(err, "清理表 %s 的字段缓存", expected.Name)
	}
	return nil
}

func (m *Manager) createIndex(ctx context.Context, metadata gnentity.Metadata, name string) error {
	for _, index := range metadata.Indexes() {
		if index.Name != name {
			continue
		}
		quotedName, err := m.dialect.Quote(index.Name)
		if err != nil {
			return err
		}
		quotedTable, err := m.dialect.Quote(metadata.Table())
		if err != nil {
			return err
		}
		columns := make([]string, 0, len(index.Fields))
		for _, fieldName := range index.Fields {
			field, exists := metadata.Field(fieldName)
			if !exists {
				return gerror.Newf("索引 %s 引用未知字段 %s", index.Name, fieldName)
			}
			column, err := m.dialect.Quote(field.Column())
			if err != nil {
				return err
			}
			columns = append(columns, column)
		}
		prefix := "CREATE INDEX"
		if index.Unique {
			prefix = "CREATE UNIQUE INDEX"
		}
		statement := fmt.Sprintf("%s %s ON %s (%s)", prefix, quotedName, quotedTable, strings.Join(columns, ", "))
		if _, err = m.database.Exec(ctx, statement); err != nil {
			return gerror.Wrapf(err, "同步创建表 %s 的索引 %s", metadata.Table(), name)
		}
		return nil
	}
	return gerror.Newf("表 %s 的索引 %s 未在 Descriptor 中找到", metadata.Table(), name)
}

func hasUnsafeDiff(metadata gnentity.Metadata, differences []Difference) bool {
	for _, difference := range differences {
		if difference.Safe {
			continue
		}
		if difference.Kind == "column" && difference.Actual == "缺失" {
			field, exists := metadata.Column(difference.Subject)
			if exists && field.Constraints().HasDefault {
				continue
			}
		}
		if !difference.Safe {
			return true
		}
	}
	return false
}

func validMode(mode Mode) bool {
	return mode == Sync || mode == Validate || mode == Off
}

// 返回完整的 Schema 差异
func Differences(err error) (Report, bool) {
	var validationError *ValidationError
	if errors.As(err, &validationError) {
		return validationError.Report, true
	}
	return Report{}, false
}
