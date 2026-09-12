package driver

// 数据库方言分类
type Kind string

const (
	MySQL      Kind = "mysql"      // MySQL 8.x
	PostgreSQL Kind = "postgresql" // PostgreSQL 9.5+
	SQLite     Kind = "sqlite"     // SQLite 3.24+
)

// 无连接状态的数据库方言
type Dialect struct {
	kind Kind
}

// 数据库语义版本
type Version struct {
	Major int // 主版本
	Minor int // 次版本
	Patch int // 修订版本
}

// 启动阶段已验证的能力集
type Capabilities struct {
	Transactions     bool // 事务
	ConditionalWrite bool // 条件写入
	RowLock          bool // 行锁
	SkipLocked       bool // 跳过已锁行
	NativeComments   bool // 原生 Schema 注释
}

// 数据库启动诊断结果
type Report struct {
	Dialect      Dialect      // 已识别方言
	Version      Version      // 已校验版本
	Capabilities Capabilities // 已验证能力
}

// 实体 Schema 的确定性语句集
type DDL struct {
	CreateTable string   // 建表语句
	Comments    []string // 表与列注释
	Indexes     []string // 普通与唯一索引
}

// 按执行依赖返回独立语句切片
func (d DDL) Statements() []string {
	statements := make([]string, 0, 1+len(d.Comments)+len(d.Indexes))
	if d.CreateTable != "" {
		statements = append(statements, d.CreateTable)
	}
	statements = append(statements, d.Comments...)
	statements = append(statements, d.Indexes...)

	return statements
}
