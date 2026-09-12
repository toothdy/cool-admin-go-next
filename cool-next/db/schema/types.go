package schema

import "github.com/toothdy/cool-admin-go-next/cool-next/db/driver"

// 管理模式
type Mode string

const (
	Sync     Mode = "sync"     // 安全增量同步
	Validate Mode = "validate" // 只校验结构
	Off      Mode = "off"      // 跳过业务实体结构
)

// 已归一化的数据库列
type Column struct {
	Name          string // 列名
	Type          string // 方言归一化类型
	Nullable      bool   // 是否允许空值
	Primary       bool   // 是否主键
	AutoIncrement bool   // 是否自增
}

// 已归一化的数据库索引
type Index struct {
	Name   string   // 索引名
	Fields []string // 有序列名
	Unique bool     // 是否唯一索引
}

// 数据库实际表结构
type Table struct {
	Name    string   // 表名
	Columns []Column // 有序列
	Indexes []Index  // 索引
}

// 不一致项
type Difference struct {
	Table    string // 实体表名
	Subject  string // 字段或索引名称
	Kind     string // 差异分类
	Expected string // 期望值
	Actual   string // 实际值
	Safe     bool   // 是否可安全增量修复
}

// 校验结果
type Report struct {
	Dialect     driver.Kind  // 数据库方言
	Differences []Difference // 确定顺序的差异
}

// 表结构不一致
type ValidationError struct {
	Report Report // 完整差异报告
}
