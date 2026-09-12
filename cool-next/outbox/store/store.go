// Package store 可靠消息的数据库持久化契约
package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"
)

// 发布状态
type Status string

const (
	Pending Status = "pending" // 等待首次发布
	Retry   Status = "retry"   // 等待重试
	Leased  Status = "leased"  // 已被 Worker 领取
	Sent    Status = "sent"    // 已发布
	Dead    Status = "dead"    // 已进入死信

	MaxListLimit = 200 // 单次运维列表查询硬上限
)

// 单次领取所有权令牌
type ClaimToken string

// 发布状态快照
type TopicStatus struct {
	Topic     string        // 消息目的地
	Status    Status        // 当前发布状态
	Count     int64         // 当前记录数
	OldestAge time.Duration // 最老记录年龄
}

// 运维查询过滤条件
type ListFilter struct {
	Status Status // 发布状态
	Topic  string // 可选消息目的地
	Limit  int    // 返回数量上限
}

// 不含消息内容和领取凭据的运维快照
type Metadata struct {
	MessageID      string     // 消息 ID
	Topic          string     // 消息目的地
	MessageType    string     // 消息契约类型
	MessageVersion uint32     // 消息版本
	Status         Status     // 当前发布状态
	Attempts       uint32     // 已持久化发布次数
	AvailableAt    time.Time  // 下次可领取时间
	LeaseExpiresAt *time.Time // 当前 Lease 截止时间
	LastError      *string    // 脱敏失败摘要
	CreateTime     time.Time  // 创建时间
	UpdateTime     time.Time  // 更新时间
	SentAt         *time.Time // 发布时间
}

// Worker 已失去记录所有权
var ErrClaimLost = gerror.New("outbox: Claim 已丢失")

// 运维目标不存在
var ErrNotFound = gerror.New("outbox: 消息不存在")

// 运维目标当前状态不可重放
var ErrReplayRejected = gerror.New("outbox: 消息不处于 dead 状态")

// 运维目标在重放期间发生并发变化
var ErrReplayConflict = gerror.New("outbox: 消息状态已并发变化")

// Store 私有字段的持久化记录
type Record struct {
	messageID      string
	topic          string
	messageType    string
	messageVersion uint32
	messageKey     *string
	payload        []byte
	headers        []byte
	status         Status
	attempts       uint32
	availableAt    time.Time
	leaseOwner     *string
	claimToken     ClaimToken
	leaseExpiresAt *time.Time
	lastError      *string
	createTime     time.Time
	updateTime     time.Time
	sentAt         *time.Time
}

// 待入队的持久化记录
func NewRecord(
	messageID string,
	topic string,
	messageType string,
	messageVersion uint32,
	messageKey *string,
	payload []byte,
	headers []byte,
) (Record, error) {
	if !validMessageID(messageID) {
		return Record{}, gerror.New("outbox: 持久化 Message ID 无效")
	}
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(messageType) == "" {
		return Record{}, gerror.New("outbox: 持久化消息元数据无效")
	}
	if messageVersion == 0 || !json.Valid(payload) || !validHeaders(headers) {
		return Record{}, gerror.New("outbox: 持久化消息内容无效")
	}
	if messageKey != nil && strings.TrimSpace(*messageKey) == "" {
		return Record{}, gerror.New("outbox: 持久化消息 Key 无效")
	}

	return Record{
		messageID:      messageID,
		topic:          topic,
		messageType:    messageType,
		messageVersion: messageVersion,
		messageKey:     cloneString(messageKey),
		payload:        append([]byte(nil), payload...),
		headers:        append([]byte(nil), headers...),
		status:         Pending,
	}, nil
}

// 消息 ID
func (record Record) MessageID() string { return record.messageID }

// 消息目的地
func (record Record) Topic() string { return record.topic }

// 消息契约类型
func (record Record) MessageType() string { return record.messageType }

// 消息版本
func (record Record) MessageVersion() uint32 { return record.messageVersion }

// 消息路由键
func (record Record) MessageKey() (string, bool) {
	if record.messageKey == nil {
		return "", false
	}

	return *record.messageKey, true
}

// 序列化载荷副本
func (record Record) Payload() []byte { return append([]byte(nil), record.payload...) }

// 序列化 Header 副本
func (record Record) Headers() []byte { return append([]byte(nil), record.headers...) }

// 发布状态
func (record Record) Status() Status { return record.status }

// 已持久化发布次数
func (record Record) Attempts() uint32 { return record.attempts }

// 下次可领取时间
func (record Record) AvailableAt() time.Time { return record.availableAt }

// 当前 Worker
func (record Record) LeaseOwner() (string, bool) {
	if record.leaseOwner == nil {
		return "", false
	}

	return *record.leaseOwner, true
}

// 当前领取令牌
func (record Record) ClaimToken() ClaimToken { return record.claimToken }

// 当前 Lease 截止时间
func (record Record) LeaseExpiresAt() (time.Time, bool) {
	if record.leaseExpiresAt == nil {
		return time.Time{}, false
	}

	return *record.leaseExpiresAt, true
}

// 脱敏失败摘要
func (record Record) LastError() (string, bool) {
	if record.lastError == nil {
		return "", false
	}

	return *record.lastError, true
}

// 创建时间
func (record Record) CreateTime() time.Time { return record.createTime }

// 更新时间
func (record Record) UpdateTime() time.Time { return record.updateTime }

// 发布时间
func (record Record) SentAt() (time.Time, bool) {
	if record.sentAt == nil {
		return time.Time{}, false
	}

	return *record.sentAt, true
}

// 数据库特定的可靠消息操作
type Store interface {
	Probe(ctx context.Context) error
	Enqueue(ctx context.Context, transaction gdb.TX, record Record) error
	Claim(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]Record, error)
	ClaimAvailable(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]Record, error)
	ClaimExpired(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]Record, error)
	Renew(ctx context.Context, messageID string, token ClaimToken, leaseDuration time.Duration) error
	MarkSent(ctx context.Context, messageID string, token ClaimToken) error
	MarkRetry(ctx context.Context, messageID string, token ClaimToken, retryAfter time.Duration, summary string) error
	MarkDead(ctx context.Context, messageID string, token ClaimToken, summary string) error
	CleanupSent(ctx context.Context, retention time.Duration, limit int) (int64, error)
	TopicStatuses(ctx context.Context) ([]TopicStatus, error)
	List(ctx context.Context, filter ListFilter) ([]Metadata, error)
	Show(ctx context.Context, messageID string) (Metadata, error)
	ReplayDead(ctx context.Context, messageID string) error
	InsertIfAbsent(ctx context.Context, transaction gdb.TX, consumer string, messageID string) (bool, error)
}

// Message ID 是否为规范的小写 UUIDv7
func IsValidMessageID(value string) bool {
	return validMessageID(value)
}

func validMessageID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if value[19] < '8' || value[19] > 'b' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}

	return true
}

func validHeaders(value []byte) bool {
	var headers map[string]string
	return json.Unmarshal(value, &headers) == nil && headers != nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
