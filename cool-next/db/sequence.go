package db

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

// 同步显式主键写入后的 PostgreSQL 自增序列
func (r *Runtime) SyncSequence(ctx context.Context, table, column string) error {
	if r == nil || r.database == nil {
		return exception.Core("框架数据库 Runtime 未初始化")
	}
	if r.dialect.Kind() != driver.PostgreSQL {
		return nil
	}
	if table == "" || column == "" {
		return exception.Core("同步自增序列缺少表名或列名")
	}
	quotedTable, err := r.dialect.Quote(table)
	if err != nil {
		return exception.WrapCore(err, "同步自增序列的表名无效")
	}
	quotedColumn, err := r.dialect.Quote(column)
	if err != nil {
		return exception.WrapCore(err, "同步自增序列的列名无效")
	}

	// pg_get_serial_sequence 对非序列列返回 NULL，无需同步
	sequence, err := r.database.GetValue(ctx, "SELECT pg_get_serial_sequence(?, ?)", table, column)
	if err != nil {
		return exception.WrapCore(err, "查询自增序列失败: "+table)
	}
	if sequence.IsNil() || sequence.String() == "" {
		return nil
	}

	// 三参数 setval(seq, value, is_called) 一条语句覆盖两种情形
	// 表非空时 value=MAX、is_called=true，下一次 nextval 返回 MAX+1
	// 表为空时 value=1、is_called=false，下一次 nextval 仍返回 1
	maximum := "(SELECT MAX(" + quotedColumn + ") FROM " + quotedTable + ")"
	statement := "SELECT setval(?::regclass, COALESCE(" + maximum + ", 1), " + maximum + " IS NOT NULL)"
	if _, err = r.database.Exec(ctx, statement, sequence.String()); err != nil {
		return exception.WrapCore(err, "同步自增序列失败: "+table)
	}

	return nil
}
