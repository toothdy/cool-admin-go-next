package outbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"slices"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db"
)

// 消费事务配置
type ConsumerConfig struct {
	ConsumerTimeout     time.Duration // 单次消费超时
	ConsumerMaxAttempts uint32        // 最大消费次数
	ConsumerRetryBase   time.Duration // 首次重试基数
	ConsumerRetryMax    time.Duration // 重试等待上限
}

// Inbox 幂等写入边界
type InboxStore interface {
	InsertIfAbsent(context.Context, gdb.TX, string, string) (bool, error)
}

// Inbox 消费事务执行器，仅保护同事务内的本地业务效果
type Deliverer struct {
	runtime       *db.Runtime
	store         InboxStore
	config        ConsumerConfig
	definitions   map[string]ConsumerDefinition
	subscriptions []Subscription
}

type consumerInvocation func(context.Context) error

// Inbox 消费事务执行器
func NewDeliverer(
	runtime *db.Runtime,
	store InboxStore,
	config ConsumerConfig,
	definitions ...ConsumerDefinition,
) (*Deliverer, error) {
	if runtime.DB() == nil || runtime.Runner() == nil || runtime.Group() == "" {
		return nil, gerror.New("outbox consumer: 框架数据库 Runtime 无效")
	}
	if store == nil {
		return nil, gerror.New("outbox consumer: Inbox Store 不能为空")
	}
	if err := checkConsumer(config); err != nil {
		return nil, err
	}
	if len(definitions) == 0 {
		return nil, gerror.New("outbox consumer: Consumer Definition 不能为空")
	}

	compiled := make(map[string]ConsumerDefinition, len(definitions))
	subscriptions := make([]Subscription, 0, len(definitions))
	for _, definition := range definitions {
		subscription, err := NewSubscription(definition)
		if err != nil {
			return nil, err
		}
		if _, exists := compiled[subscription.Name()]; exists {
			return nil, gerror.Newf("outbox consumer: Consumer Name %q 重复", subscription.Name())
		}
		compiled[subscription.Name()] = definition
		subscriptions = append(subscriptions, subscription)
	}

	return &Deliverer{
		runtime:       runtime,
		store:         store,
		config:        config,
		definitions:   compiled,
		subscriptions: subscriptions,
	}, nil
}

// 不可变订阅列表副本
func (deliverer *Deliverer) Subscriptions() []Subscription {
	if deliverer == nil {
		return nil
	}

	return append([]Subscription(nil), deliverer.subscriptions...)
}

// 一次持久化 Attempt 对应的消费
func (deliverer *Deliverer) Deliver(
	ctx context.Context,
	subscription Subscription,
	message Envelope,
	attempt uint32,
) DeliveryDecision {
	if deliverer == nil {
		return DeadLetter(gerror.New("outbox consumer: Deliverer 不能为空"))
	}
	if attempt == 0 {
		return DeadLetter(gerror.New("outbox consumer: Attempt 必须从 1 开始"))
	}
	if attempt > deliverer.config.ConsumerMaxAttempts {
		return DeadLetter(gerror.Newf("outbox consumer: Attempt %d 已超过消费上限", attempt))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, deliverer.config.ConsumerTimeout)
	defer cancel()

	definition, err := deliverer.definition(subscription)
	if err != nil {
		return DeadLetter(err)
	}
	validated, err := checkEnvelope(subscription, message)
	if err != nil {
		return DeadLetter(err)
	}
	invoke, err := definition.decode(validated)
	if err != nil {
		return DeadLetter(err)
	}

	err = deliverer.runtime.Runner().Within(deliveryCtx, func(scopeCtx context.Context) error {
		transaction, exists, currentErr := deliverer.runtime.Current(scopeCtx)
		if currentErr != nil {
			return gerror.Wrap(currentErr, "outbox consumer: 读取消费事务")
		}
		if !exists || transaction == nil {
			return gerror.New("outbox consumer: 消费事务无效")
		}
		inserted, insertErr := deliverer.store.InsertIfAbsent(
			scopeCtx,
			transaction,
			subscription.Name(),
			string(validated.MessageID()),
		)
		if insertErr != nil {
			return gerror.Wrap(insertErr, "outbox consumer: 写入 Inbox")
		}
		if !inserted {
			return nil
		}
		if invokeErr := invoke(scopeCtx); invokeErr != nil {
			return invokeErr
		}

		return scopeCtx.Err()
	})
	if err == nil {
		return Ack()
	}
	if isPermanent(err) || attempt >= deliverer.config.ConsumerMaxAttempts {
		return DeadLetter(err)
	}
	delay, delayErr := consumerDelay(deliverer.config, attempt)
	if delayErr != nil {
		return Retry(0, gerror.Wrap(delayErr, "outbox consumer: 生成重试延迟"))
	}

	return Retry(delay, err)
}

func (deliverer *Deliverer) definition(subscription Subscription) (ConsumerDefinition, error) {
	definition, exists := deliverer.definitions[subscription.Name()]
	if !exists {
		return nil, gerror.Newf("outbox consumer: Subscription %q 未注册", subscription.Name())
	}
	compiled := definition.subscription()
	if compiled.Topic() != subscription.Topic() || compiled.MessageType() != subscription.MessageType() ||
		!slices.Equal(compiled.SupportedVersions(), subscription.SupportedVersions()) {
		return nil, gerror.Newf("outbox consumer: Subscription %q 与注册定义不匹配", subscription.Name())
	}

	return definition, nil
}

func (definition consumerDefinition[T]) decode(message Envelope) (consumerInvocation, error) {
	var payload T
	if err := json.Unmarshal(message.Payload(), &payload); err != nil {
		return nil, gerror.Wrap(err, "outbox consumer: 解码消息 DTO")
	}
	incoming := Incoming[T]{envelope: message, payload: payload}

	return func(ctx context.Context) error {
		return definition.handler(ctx, incoming)
	}, nil
}

func checkEnvelope(subscription Subscription, message Envelope) (Envelope, error) {
	var key *string
	if value, exists := message.Key(); exists {
		key = &value
	}
	validated, err := Restore(
		message.MessageID(),
		message.Topic(),
		message.MessageType(),
		message.Version(),
		key,
		message.Payload(),
		message.Headers(),
	)
	if err != nil {
		return Envelope{}, gerror.Wrap(err, "outbox consumer: Envelope 无效")
	}
	if validated.Topic() != subscription.Topic() {
		return Envelope{}, gerror.New("outbox consumer: Envelope Topic 与 Subscription 不匹配")
	}
	if validated.MessageType() != subscription.MessageType() {
		return Envelope{}, gerror.New("outbox consumer: Envelope Message Type 与 Subscription 不匹配")
	}
	if !slices.Contains(subscription.SupportedVersions(), validated.Version()) {
		return Envelope{}, gerror.Newf("outbox consumer: 不支持消息版本 %d", validated.Version())
	}

	return validated, nil
}

func checkConsumer(config ConsumerConfig) error {
	if config.ConsumerTimeout <= 0 || config.ConsumerMaxAttempts == 0 ||
		config.ConsumerRetryBase <= 0 || config.ConsumerRetryMax <= 0 {
		return gerror.New("outbox consumer: 配置值必须为正数")
	}
	if config.ConsumerRetryBase > config.ConsumerRetryMax {
		return gerror.New("outbox consumer: Retry Base 不能大于 Retry Max")
	}

	return nil
}

func consumerDelay(config ConsumerConfig, attempt uint32) (time.Duration, error) {
	delay := config.ConsumerRetryBase
	for current := uint32(1); current < attempt && delay < config.ConsumerRetryMax; current++ {
		if delay > config.ConsumerRetryMax/2 {
			delay = config.ConsumerRetryMax
			break
		}
		delay *= 2
	}
	if delay > config.ConsumerRetryMax {
		delay = config.ConsumerRetryMax
	}
	floor := delay / 2
	span := delay - floor
	random, err := rand.Int(rand.Reader, big.NewInt(int64(span)+1))
	if err != nil {
		return 0, err
	}

	return floor + time.Duration(random.Int64()), nil
}
