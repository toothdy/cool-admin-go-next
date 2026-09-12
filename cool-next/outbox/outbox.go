// Package outbox 可靠消息的不可变契约
package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

const (
	defaultVersion   uint32 = 1
	maxUUIDTimestamp uint64 = (1 << 48) - 1
)

var allowedHeaders = map[string]struct{}{
	"content-type": {},
	"traceparent":  {},
	"tracestate":   {},
	"x-trace-id":   {},
}

// RFC 9562 UUIDv7 的小写标准文本形式
type MessageID string

// 不可变的消息内容
type Envelope struct {
	messageID   MessageID
	topic       string
	messageType string
	version     uint32
	key         *string
	payload     []byte
	headers     map[string]string
}

type messageOptions struct {
	key     *string
	version uint32
	headers map[string]string
}

// 消息创建参数修改器
type Option func(*messageOptions) error

// 新的消息
func New[T any](topic, messageType string, payload T, options ...Option) (Envelope, error) {
	if err := checkText("Topic", topic); err != nil {
		return Envelope{}, err
	}
	if err := checkText("Message Type", messageType); err != nil {
		return Envelope{}, err
	}

	settings := messageOptions{version: defaultVersion, headers: make(map[string]string)}
	for _, option := range options {
		if option == nil {
			return Envelope{}, fmt.Errorf("outbox: 消息选项不能为空")
		}
		if err := option(&settings); err != nil {
			return Envelope{}, err
		}
	}
	if settings.version == 0 {
		return Envelope{}, fmt.Errorf("outbox:消息版本必须为正整数")
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("outbox: 消息载荷序列化失败: %w", err)
	}

	messageID, err := newMessageID(time.Now, rand.Reader)
	if err != nil {
		return Envelope{}, err
	}

	return newEnvelope(messageID, topic, messageType, settings.version, settings.key, encoded, settings.headers)
}

// 消息路由键
func WithKey(key string) Option {
	return func(settings *messageOptions) error {
		if strings.TrimSpace(key) != key || key == "" {
			return fmt.Errorf("outbox: 消息 Key 无效")
		}
		copyKey := key
		settings.key = &copyKey
		return nil
	}
}

// 消息版本
func WithVersion(version uint32) Option {
	return func(settings *messageOptions) error {
		if version == 0 {
			return fmt.Errorf("outbox: 消息版本必须为正整数")
		}
		settings.version = version
		return nil
	}
}

// 允许的传输元数据
func WithHeader(name, value string) Option {
	return func(settings *messageOptions) error {
		normalizedName, err := headerName(name)
		if err != nil {
			return err
		}
		if !validHeaderValue(value) {
			return fmt.Errorf("outbox: Header 值无效")
		}
		if _, exists := settings.headers[normalizedName]; exists {
			return fmt.Errorf("outbox: Header %q 重复", normalizedName)
		}
		settings.headers[normalizedName] = value
		return nil
	}
}

// 消息 ID
func (message Envelope) MessageID() MessageID { return message.messageID }

// 消息目的地
func (message Envelope) Topic() string { return message.topic }

// 消息契约类型
func (message Envelope) MessageType() string { return message.messageType }

// 消息版本
func (message Envelope) Version() uint32 { return message.version }

// 消息路由键
func (message Envelope) Key() (string, bool) {
	if message.key == nil {
		return "", false
	}
	return *message.key, true
}

// 消息载荷副本
func (message Envelope) Payload() []byte { return append([]byte(nil), message.payload...) }

// 消息 Header 副本
func (message Envelope) Headers() map[string]string {
	return cloneHeaders(message.headers)
}

// 需要可靠投递的消息
type Enqueuer interface {
	Enqueue(context.Context, Envelope) error
}

// 投递出口
type Publisher interface {
	Publish(context.Context, Envelope) error
}

// 解码后的消息
type Incoming[T any] struct {
	envelope Envelope
	payload  T
}

// 消息 ID
func (message Incoming[T]) MessageID() MessageID { return message.envelope.MessageID() }

// 消息目的地
func (message Incoming[T]) Topic() string { return message.envelope.Topic() }

// 消息契约类型
func (message Incoming[T]) MessageType() string { return message.envelope.MessageType() }

// 消息版本
func (message Incoming[T]) Version() uint32 { return message.envelope.Version() }

// 消息路由键
func (message Incoming[T]) Key() (string, bool) { return message.envelope.Key() }

// 已解码的消息载荷
func (message Incoming[T]) Payload() T { return message.payload }

// 消息 Header 副本
func (message Incoming[T]) Headers() map[string]string { return message.envelope.Headers() }

// 消费回调函数
type ConsumerHandler[T any] func(context.Context, Incoming[T]) error

// 编译期消费描述
type ConsumerDefinition interface {
	consumerDefinition()
	subscription() Subscription
	decode(Envelope) (consumerInvocation, error)
}

type consumerDefinition[T any] struct {
	name              string
	topic             string
	messageType       string
	supportedVersions []uint32
	handler           ConsumerHandler[T]
}

func (consumerDefinition[T]) consumerDefinition() {}

func (definition consumerDefinition[T]) subscription() Subscription {
	return newSubscription(definition.name, definition.topic, definition.messageType, definition.supportedVersions)
}

// 消费注册定义
func Consume[T any](name, topic, messageType string, supportedVersions []uint32, handler ConsumerHandler[T]) (ConsumerDefinition, error) {
	if err := checkConsumerName(name); err != nil {
		return nil, err
	}
	if err := checkText("Topic", topic); err != nil {
		return nil, err
	}
	if err := checkText("Message Type", messageType); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, fmt.Errorf("outbox: Consumer Handler 不能为空")
	}
	supported, err := versions(supportedVersions)
	if err != nil {
		return nil, err
	}

	return consumerDefinition[T]{
		name:              name,
		topic:             topic,
		messageType:       messageType,
		supportedVersions: supported,
		handler:           handler,
	}, nil
}

// 不可变订阅
func NewSubscription(definition ConsumerDefinition) (Subscription, error) {
	if definition == nil {
		return Subscription{}, fmt.Errorf("outbox: Consumer Definition 不能为空")
	}
	return definition.subscription(), nil
}

// 不可变消费订阅
type Subscription struct {
	name              string
	topic             string
	messageType       string
	supportedVersions []uint32
}

// Consumer 名
func (subscription Subscription) Name() string { return subscription.name }

// 订阅目的地
func (subscription Subscription) Topic() string { return subscription.topic }

// 订阅契约类型
func (subscription Subscription) MessageType() string { return subscription.messageType }

// 支持版本副本
func (subscription Subscription) SupportedVersions() []uint32 {
	return append([]uint32(nil), subscription.supportedVersions...)
}

func newSubscription(name, topic, messageType string, versions []uint32) Subscription {
	return Subscription{
		name:              name,
		topic:             topic,
		messageType:       messageType,
		supportedVersions: append([]uint32(nil), versions...),
	}
}

// 消费结果类别
type DeliveryDisposition string

const (
	DeliveryAck        DeliveryDisposition = "ack"         // 确认
	DeliveryRetry      DeliveryDisposition = "retry"       // 重试
	DeliveryDeadLetter DeliveryDisposition = "dead-letter" // 死信
)

// 消费结果
type DeliveryDecision struct {
	disposition DeliveryDisposition
	retryAfter  time.Duration
	err         error
}

// 确认结果
func Ack() DeliveryDecision { return DeliveryDecision{disposition: DeliveryAck} }

// 临时失败结果
func Retry(after time.Duration, err error) DeliveryDecision {
	if after < 0 {
		after = 0
	}
	return DeliveryDecision{disposition: DeliveryRetry, retryAfter: after, err: err}
}

// 永久失败结果
func DeadLetter(err error) DeliveryDecision {
	return DeliveryDecision{disposition: DeliveryDeadLetter, err: err}
}

// 消费结果类别
func (decision DeliveryDecision) Disposition() DeliveryDisposition { return decision.disposition }

// 建议重试延迟
func (decision DeliveryDecision) RetryAfter() time.Duration { return decision.retryAfter }

// 消费错误
func (decision DeliveryDecision) Error() error { return decision.err }

// 一次持久化 Attempt 对应的消费
type DeliverFunc func(context.Context, Subscription, Envelope, uint32) DeliveryDecision

// 可靠消费能力集合
type ConsumerCapabilities struct {
	DurableAck           bool
	DurableRetryAttempts bool
	DelayedRetry         bool
	DeadLetter           bool
	PreservesMessageID   bool
	MaxEnvelopeBytes     int
}

// 可靠消息消费能力
type ConsumerAdapter interface {
	Name() string
	Capabilities(context.Context) (ConsumerCapabilities, error)
	Prepare(context.Context, []Subscription, DeliverFunc) error
	Start(context.Context) (<-chan error, error)
	Stop(context.Context) error
}

type permanentError struct{ cause error }

// 不可重试错误包装
func Permanent(cause error) error {
	if cause == nil {
		return nil
	}
	return permanentError{cause: cause}
}

// 当前错误描述
func (err permanentError) Error() string { return err.cause.Error() }

// 解包后的原始错误
func (err permanentError) Unwrap() error { return err.cause }

func isPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

// 由持久字段重建 Envelope
func Restore(messageID MessageID, topic, messageType string, version uint32, key *string, payload []byte, headers map[string]string) (Envelope, error) {
	if err := checkMessageID(messageID); err != nil {
		return Envelope{}, err
	}
	if err := checkText("Topic", topic); err != nil {
		return Envelope{}, err
	}
	if err := checkText("Message Type", messageType); err != nil {
		return Envelope{}, err
	}
	if version == 0 {
		return Envelope{}, fmt.Errorf("outbox: 消息版本必须为正整数")
	}
	if key != nil {
		if err := checkKey(*key); err != nil {
			return Envelope{}, err
		}
	}
	if !json.Valid(payload) {
		return Envelope{}, fmt.Errorf("outbox: 消息载荷不是有效 JSON")
	}
	normalizedHeaders, err := cleanHeaders(headers)
	if err != nil {
		return Envelope{}, err
	}

	return newEnvelope(messageID, topic, messageType, version, key, payload, normalizedHeaders)
}

func newEnvelope(messageID MessageID, topic, messageType string, version uint32, key *string, payload []byte, headers map[string]string) (Envelope, error) {
	if err := checkMessageID(messageID); err != nil {
		return Envelope{}, err
	}
	if version == 0 || !json.Valid(payload) {
		return Envelope{}, fmt.Errorf("outbox: 消息内容无效")
	}

	var copiedKey *string
	if key != nil {
		value := *key
		copiedKey = &value
	}
	return Envelope{
		messageID:   messageID,
		topic:       topic,
		messageType: messageType,
		version:     version,
		key:         copiedKey,
		payload:     append([]byte(nil), payload...),
		headers:     cloneHeaders(headers),
	}, nil
}

func checkText(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("outbox: %s 无效", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return fmt.Errorf("outbox: %s 无效", label)
		}
	}
	return nil
}

func checkKey(value string) error {
	if strings.TrimSpace(value) != value || value == "" || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("outbox: 消息 Key 无效")
	}
	return nil
}

func checkConsumerName(value string) error {
	if err := checkText("Consumer Name", value); err != nil {
		return err
	}
	if strings.HasPrefix(value, "__cool_") {
		return fmt.Errorf("outbox: Consumer Name 使用了保留前缀")
	}
	return nil
}

func versions(values []uint32) ([]uint32, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("outbox: Consumer 支持版本不能为空")
	}
	result := append([]uint32(nil), values...)
	seen := make(map[uint32]struct{}, len(result))
	for _, value := range result {
		if value == 0 {
			return nil, fmt.Errorf("outbox: Consumer 支持版本必须为正整数")
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("outbox: Consumer 支持版本重复")
		}
		seen[value] = struct{}{}
	}
	return result, nil
}

func headerName(name string) (string, error) {
	if strings.TrimSpace(name) != name || name == "" {
		return "", fmt.Errorf("outbox: Header 名称无效")
	}
	normalized := strings.ToLower(name)
	if _, exists := allowedHeaders[normalized]; !exists {
		return "", fmt.Errorf("outbox: Header %q 不在允许列表中", name)
	}
	return normalized, nil
}

func validHeaderValue(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func cleanHeaders(values map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for name, value := range values {
		normalizedName, err := headerName(name)
		if err != nil {
			return nil, err
		}
		if !validHeaderValue(value) {
			return nil, fmt.Errorf("outbox: Header 值无效")
		}
		if _, exists := result[normalizedName]; exists {
			return nil, fmt.Errorf("outbox: Header %q 重复", normalizedName)
		}
		result[normalizedName] = value
	}
	return result, nil
}

func cloneHeaders(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}

func newMessageID(now func() time.Time, source io.Reader) (MessageID, error) {
	if now == nil || source == nil {
		return "", fmt.Errorf("outbox: UUIDv7 依赖不能为空")
	}
	milliseconds := now().UnixMilli()
	if milliseconds < 0 || uint64(milliseconds) > maxUUIDTimestamp {
		return "", fmt.Errorf("outbox: UUIDv7 时间戳超出范围")
	}

	var randomBytes [10]byte
	if _, err := io.ReadFull(source, randomBytes[:]); err != nil {
		return "", fmt.Errorf("outbox: UUIDv7 随机源读取失败: %w", err)
	}

	var value [16]byte
	timestamp := uint64(milliseconds)
	value[0] = byte(timestamp >> 40)
	value[1] = byte(timestamp >> 32)
	value[2] = byte(timestamp >> 24)
	value[3] = byte(timestamp >> 16)
	value[4] = byte(timestamp >> 8)
	value[5] = byte(timestamp)
	value[6] = randomBytes[0]&0x0f | 0x70
	value[7] = randomBytes[1]
	value[8] = randomBytes[2]&0x3f | 0x80
	copy(value[9:], randomBytes[3:])

	var text [36]byte
	hex.Encode(text[0:8], value[0:4])
	hex.Encode(text[9:13], value[4:6])
	hex.Encode(text[14:18], value[6:8])
	hex.Encode(text[19:23], value[8:10])
	hex.Encode(text[24:36], value[10:16])
	text[8] = '-'
	text[13] = '-'
	text[18] = '-'
	text[23] = '-'
	return MessageID(text[:]), nil
}

func checkMessageID(value MessageID) error {
	text := string(value)
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' || text[14] != '7' {
		return fmt.Errorf("outbox: Message ID 不是有效 UUIDv7")
	}
	if text[19] < '8' || text[19] > 'b' {
		return fmt.Errorf("outbox: Message ID Variant 无效")
	}
	for index, character := range text {
		if character == '-' {
			continue
		}
		if character >= 'A' && character <= 'F' {
			return fmt.Errorf("outbox: Message ID 必须使用小写")
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return fmt.Errorf("outbox: Message ID 不是有效 UUIDv7: 位置 %d", index)
		}
	}
	return nil
}
