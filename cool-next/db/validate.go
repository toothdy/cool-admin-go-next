package db

import (
	"sort"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"

	"fmt"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

func checkConfig(config Config) (gdb.ConfigGroup, []string, error) {
	if strings.TrimSpace(config.Group) == "" {
		return nil, nil, exception.Core("框架数据库组不能为空")
	}
	if len(config.Nodes) == 0 {
		return nil, nil, exception.Core(fmt.Sprintf("框架数据库组 %s 没有连接节点", config.Group))
	}
	dialect, err := driver.New(driver.SQLite)
	if err != nil {
		return nil, nil, exception.WrapCore(err, "创建标识符校验方言")
	}
	tables := make([]string, 0, len(config.TransactionTables))
	seen := make(map[string]struct{}, len(config.TransactionTables))
	for _, table := range config.TransactionTables {
		if _, err = dialect.Quote(table); err != nil {
			return nil, nil, exception.WrapCore(err, "事务表名无效")
		}
		if _, exists := seen[table]; exists {
			return nil, nil, exception.Core(fmt.Sprintf("事务表名重复: %s", table))
		}
		seen[table] = struct{}{}
		tables = append(tables, table)
	}
	sort.Strings(tables)

	return append(gdb.ConfigGroup(nil), config.Nodes...), tables, nil
}
