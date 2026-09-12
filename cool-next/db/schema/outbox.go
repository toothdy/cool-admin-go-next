package schema

import "github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"

const (
	OutboxTableName = "cool_outbox" // 发件箱表名
	InboxTableName  = "cool_inbox"  // 收件箱表名

	OutboxAvailableIndex = "idx_cool_outbox_available" // 可投递消息索引
	OutboxLeaseIndex     = "idx_cool_outbox_lease"     // 投递租约索引
	OutboxSentIndex      = "idx_cool_outbox_sent"      // 已投递消息索引
)

// 内部表字段定义
type DefinitionColumn struct {
	Name          string               // 列名
	Type          gnentity.LogicalType // 跨数据库逻辑类型
	Size          uint64               // 固定最大长度
	Nullable      bool                 // 是否允许空值
	CaseSensitive bool                 // 是否区分大小写
	CharacterSet  string               // 逻辑字符集
	Default       string               // 默认值或数据库表达式
	AllowedValues []string             // 有限字符串取值
}

// 内部表逻辑结构
type Definition struct {
	Name        string             // 表名
	Description string             // 表用途
	Columns     []DefinitionColumn // 有序列定义
	PrimaryKey  []string           // 有序主键列
	Indexes     []Index            // 有序固定索引
}

// 可靠发布记录表
func OutboxDefinition() Definition {
	return cloneDefinition(outboxDefinition)
}

// 消费幂等标记表
func InboxDefinition() Definition {
	return cloneDefinition(inboxDefinition)
}

var outboxDefinition = Definition{
	Name:        OutboxTableName,
	Description: "可靠消息发布记录",
	Columns: []DefinitionColumn{
		{Name: "messageId", Type: gnentity.LogicalString, Size: 36, CaseSensitive: true, CharacterSet: "ascii"},
		{Name: "topic", Type: gnentity.LogicalString},
		{Name: "messageType", Type: gnentity.LogicalString},
		{Name: "messageVersion", Type: gnentity.LogicalUint},
		{Name: "messageKey", Type: gnentity.LogicalString, Nullable: true},
		{Name: "payload", Type: gnentity.LogicalBytes},
		{Name: "headers", Type: gnentity.LogicalBytes},
		{Name: "status", Type: gnentity.LogicalString, Default: "pending", AllowedValues: []string{"pending", "retry", "leased", "sent", "dead"}},
		{Name: "attempts", Type: gnentity.LogicalUint, Default: "0"},
		{Name: "availableAt", Type: gnentity.LogicalTime, Default: "CURRENT_TIMESTAMP"},
		{Name: "leaseOwner", Type: gnentity.LogicalString, Nullable: true},
		{Name: "claimToken", Type: gnentity.LogicalString, Nullable: true},
		{Name: "leaseExpiresAt", Type: gnentity.LogicalTime, Nullable: true},
		{Name: "lastError", Type: gnentity.LogicalString, Nullable: true},
		{Name: "createTime", Type: gnentity.LogicalTime, Default: "CURRENT_TIMESTAMP"},
		{Name: "updateTime", Type: gnentity.LogicalTime, Default: "CURRENT_TIMESTAMP"},
		{Name: "sentAt", Type: gnentity.LogicalTime, Nullable: true},
	},
	PrimaryKey: []string{"messageId"},
	Indexes: []Index{
		{Name: OutboxAvailableIndex, Fields: []string{"status", "availableAt", "createTime", "messageId"}},
		{Name: OutboxLeaseIndex, Fields: []string{"status", "leaseExpiresAt", "messageId"}},
		{Name: OutboxSentIndex, Fields: []string{"status", "sentAt", "messageId"}},
	},
}

var inboxDefinition = Definition{
	Name:        InboxTableName,
	Description: "可靠消息消费幂等标记",
	Columns: []DefinitionColumn{
		{Name: "consumer", Type: gnentity.LogicalString, CaseSensitive: true},
		{Name: "messageId", Type: gnentity.LogicalString, Size: 36, CaseSensitive: true, CharacterSet: "ascii"},
		{Name: "processedAt", Type: gnentity.LogicalTime, Default: "CURRENT_TIMESTAMP"},
	},
	PrimaryKey: []string{"consumer", "messageId"},
}

func cloneDefinition(source Definition) Definition {
	result := source
	result.Columns = append([]DefinitionColumn(nil), source.Columns...)
	for index := range result.Columns {
		result.Columns[index].AllowedValues = append([]string(nil), source.Columns[index].AllowedValues...)
	}
	result.PrimaryKey = append([]string(nil), source.PrimaryKey...)
	result.Indexes = append([]Index(nil), source.Indexes...)
	for index := range result.Indexes {
		result.Indexes[index].Fields = append([]string(nil), source.Indexes[index].Fields...)
	}

	return result
}
