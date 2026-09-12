package db

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"

	"fmt"
)

// 框架数据库组启动配置
type Config struct {
	Group             string          // cool.outbox.databaseGroup
	Nodes             gdb.ConfigGroup // 同名 GoFrame 连接节点
	TransactionTables []string        // 需保障事务能力的表
}

// 框架数据库组运行时
type Runtime struct {
	group      string
	database   gdb.DB
	dialect    driver.Dialect
	runner     tx.Runner
	diagnostic Diagnostic
}

// 创建并探测 Framework Database Group
func New(ctx context.Context, config Config) (*Runtime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodes, tables, err := checkConfig(config)
	if err != nil {
		return nil, err
	}
	if err = gdb.SetConfigGroup(config.Group, nodes); err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("注册框架数据库组 %s", config.Group))
	}
	database, err := gdb.Instance(config.Group)
	if err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("获取框架数据库组 %s", config.Group))
	}
	if database.GetGroup() != config.Group {
		return nil, exception.Core(fmt.Sprintf("框架数据库组不匹配: 期望 %s，实际 %s", config.Group, database.GetGroup()))
	}
	report, err := driver.Probe(ctx, database, tables...)
	if err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("探测框架数据库组 %s", config.Group))
	}
	runner, err := tx.NewRunner(config.Group, database)
	if err != nil {
		return nil, exception.WrapCore(err, "创建框架数据库事务 Runner")
	}

	return &Runtime{
		group:      config.Group,
		database:   database,
		dialect:    report.Dialect,
		runner:     runner,
		diagnostic: newDiagnostic(config.Group, report, tables),
	}, nil
}

// 返回框架数据库组
func (r *Runtime) Group() string {
	if r == nil {
		return ""
	}

	return r.group
}

// 返回框架数据库对象
func (r *Runtime) DB() gdb.DB {
	if r == nil {
		return nil
	}

	return r.database
}

// 返回框架数据库方言
func (r *Runtime) Dialect() driver.Dialect {
	if r == nil {
		return driver.Dialect{}
	}

	return r.dialect
}

// 返回框架事务 Runner
func (r *Runtime) Runner() tx.Runner {
	if r == nil {
		return nil
	}

	return r.runner
}

// 查询当前同组事务
func (r *Runtime) Current(ctx context.Context) (gdb.TX, bool, error) {
	if r == nil || r.database == nil {
		return nil, false, exception.Core("框架数据库 Runtime 未初始化")
	}
	transaction, group, exists := tx.Current(ctx)
	if !exists {
		return nil, false, nil
	}
	if group != r.group {
		return nil, false, exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", group, r.group))
	}

	return transaction, true, nil
}

// 返回无敏感信息的启动诊断
func (r *Runtime) Diagnostic() Diagnostic {
	if r == nil {
		return Diagnostic{}
	}

	return cloneDiagnostic(r.diagnostic)
}
