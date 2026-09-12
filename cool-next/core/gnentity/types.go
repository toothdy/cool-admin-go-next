package gnentity

import "reflect"

// 跨数据库的逻辑类型
type LogicalType string

const (
	LogicalBool   LogicalType = "bool"   // 布尔值
	LogicalInt    LogicalType = "int"    // 有符号整数
	LogicalUint   LogicalType = "uint"   // 无符号整数
	LogicalFloat  LogicalType = "float"  // 浮点数
	LogicalString LogicalType = "string" // 字符串
	LogicalBytes  LogicalType = "bytes"  // 字节数组
	LogicalJSON   LogicalType = "json"   // JSON 结构
	LogicalTime   LogicalType = "time"   // 时间
)

// 可移植字段约束
type Constraints struct {
	Size         uint64 // 字符串/字节最大长度
	HasSize      bool
	Default      string // 默认值
	HasDefault   bool
	Precision    uint64 // 浮点精度
	HasPrecision bool
	Scale        uint64 // 浮点小数位数
	HasScale     bool
}

// 实体字段的只读元数据
type Field interface {
	Name() string             // 逻辑名
	JSONName() string         // JSON 字段名
	Column() string           // 数据库列名
	Description() string      // 字段描述
	LogicalType() LogicalType // 逻辑类型
	GoType() reflect.Type     // Go 类型
	Nullable() bool           // 是否可为空
	Primary() bool            // 是否主键
	AutoIncrement() bool      // 是否自增
	SystemMaintained() bool   // 是否系统维护
	Persistent() bool         // 是否持久化
	Constraints() Constraints // cool 约束
}

// 实体的只读元数据
type Metadata interface {
	Table() string                    // 表名
	Description() string              // 表描述
	Primary() Field                   // 主键
	Fields() []Field                  // 字段列表
	PersistentFields() []Field        // 持久化字段列表
	Field(name string) (Field, bool)  // 按逻辑名查字段
	JSON(name string) (Field, bool)   // 按 JSON 名查字段
	Column(name string) (Field, bool) // 按列名查字段
	Indexes() []Index                 // 索引列表
}

// 数据库写入值,支持四态跟踪
type DOValue interface {
	Has(field string) bool
	IsNull(field string) bool
	SetColumn(field string, value any) error
	DBData() any
}

// 可由运行期注册表统一管理的实体 Descriptor
type RuntimeDescriptor interface {
	Metadata
	EntityType() reflect.Type // 实体 Go 类型
	IDType() reflect.Type     // 主键 Go 类型
	NewDO() DOValue           // 创建独立数据库写入值
}

// 实体类型、主键类型与只读元数据的绑定
type Descriptor[E any, ID comparable] interface {
	RuntimeDescriptor
}
