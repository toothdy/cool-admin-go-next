package outbox

import (
	"context"
	"encoding/json"

	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/outbox/store"
)

// Store 状态
type Status = store.Status

const (
	Pending  = store.Pending // 等待首次发布
	Retrying = store.Retry   // 等待重试
	Leased   = store.Leased  // 已被 Worker 领取
	Sent     = store.Sent    // 已发布
	Dead     = store.Dead    // 已进入死信
)

// 单次领取所有权令牌
type ClaimToken = store.ClaimToken

// Worker 已失去记录所有权
var ErrClaimLost = store.ErrClaimLost

// 入队消息大小限制
type EnqueueLimits struct {
	MaxPayloadBytes  int // 载荷上限
	MaxHeaderBytes   int // Header 上限
	MaxEnvelopeBytes int // 完整消息上限
}

type databaseEnqueuer struct {
	runtime *db.Runtime
	store   store.Store
	limits  EnqueueLimits
}

type serializedEnvelope struct {
	MessageID   MessageID         `json:"messageId"`
	Topic       string            `json:"topic"`
	MessageType string            `json:"messageType"`
	Version     uint32            `json:"version"`
	Key         *string           `json:"key,omitempty"`
	Payload     json.RawMessage   `json:"payload"`
	Headers     map[string]string `json:"headers"`
}

// 数据库 Enqueuer
func NewEnqueuer(runtime *db.Runtime, store store.Store, limits EnqueueLimits) (Enqueuer, error) {
	if runtime.DB() == nil || runtime.Runner() == nil || runtime.Group() == "" {
		return nil, gerror.New("outbox: 框架数据库 Runtime 无效")
	}
	if store == nil {
		return nil, gerror.New("outbox: Store 不能为空")
	}
	if limits.MaxPayloadBytes <= 0 || limits.MaxHeaderBytes <= 0 || limits.MaxEnvelopeBytes <= 0 {
		return nil, gerror.New("outbox: 入队消息大小限制必须为正数")
	}

	return &databaseEnqueuer{runtime: runtime, store: store, limits: limits}, nil
}

// 框架事务内落库待发消息
func (enqueuer *databaseEnqueuer) Enqueue(ctx context.Context, message Envelope) error {
	record, err := enqueuer.toRecord(message)
	if err != nil {
		return err
	}

	return enqueuer.runtime.Runner().Within(ctx, func(scopeCtx context.Context) error {
		transaction, exists, err := enqueuer.runtime.Current(scopeCtx)
		if err != nil {
			return gerror.Wrap(err, "outbox: 读取入队事务")
		}
		if !exists || transaction == nil {
			return gerror.New("outbox: 入队事务无效")
		}
		if err = enqueuer.store.Enqueue(scopeCtx, transaction, record); err != nil {
			return gerror.Wrap(err, "outbox: 写入消息")
		}

		return nil
	})
}

func (enqueuer *databaseEnqueuer) toRecord(message Envelope) (store.Record, error) {
	payload := message.Payload()
	if len(payload) > enqueuer.limits.MaxPayloadBytes {
		return store.Record{}, gerror.Newf(
			"outbox: Payload 超出大小上限: %d > %d",
			len(payload),
			enqueuer.limits.MaxPayloadBytes,
		)
	}
	headers := message.Headers()
	encodedHeaders, err := json.Marshal(headers)
	if err != nil {
		return store.Record{}, gerror.Wrap(err, "outbox: 序列化 Header")
	}
	if len(encodedHeaders) > enqueuer.limits.MaxHeaderBytes {
		return store.Record{}, gerror.Newf(
			"outbox: Header 超出大小上限: %d > %d",
			len(encodedHeaders),
			enqueuer.limits.MaxHeaderBytes,
		)
	}

	var key *string
	if value, exists := message.Key(); exists {
		key = &value
	}
	encodedEnvelope, err := json.Marshal(serializedEnvelope{
		MessageID:   message.MessageID(),
		Topic:       message.Topic(),
		MessageType: message.MessageType(),
		Version:     message.Version(),
		Key:         key,
		Payload:     payload,
		Headers:     headers,
	})
	if err != nil {
		return store.Record{}, gerror.Wrap(err, "outbox: 序列化完整消息")
	}
	if len(encodedEnvelope) > enqueuer.limits.MaxEnvelopeBytes {
		return store.Record{}, gerror.Newf(
			"outbox: Envelope 超出大小上限: %d > %d",
			len(encodedEnvelope),
			enqueuer.limits.MaxEnvelopeBytes,
		)
	}

	return store.NewRecord(
		string(message.MessageID()),
		message.Topic(),
		message.MessageType(),
		message.Version(),
		key,
		payload,
		encodedHeaders,
	)
}

func envelope(record store.Record) (Envelope, error) {
	var headers map[string]string
	if err := json.Unmarshal(record.Headers(), &headers); err != nil {
		return Envelope{}, gerror.Wrap(err, "outbox: 解析持久化 Header")
	}
	var key *string
	if value, exists := record.MessageKey(); exists {
		key = &value
	}

	return Restore(
		MessageID(record.MessageID()),
		record.Topic(),
		record.MessageType(),
		record.MessageVersion(),
		key,
		record.Payload(),
		headers,
	)
}
