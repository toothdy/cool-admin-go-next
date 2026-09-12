package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
)

// Broker 消费适配配置
type BrokerConsumerConfig struct {
	ConsumerTimeout  time.Duration // 单次消费超时
	MaxEnvelopeBytes int           // 完整消息上限
}

// Broker 原始投递
type BrokerDelivery interface {
	MessageID() MessageID
	Topic() string
	MessageType() string
	Version() uint32
	Key() (string, bool)
	Payload() []byte
	Headers() map[string]string
}

// Broker 投递接收函数
type BrokerReceiveFunc func(context.Context, Subscription, BrokerDelivery)

// DLQ 记录
type ConsumerDeadLetter struct {
	ConsumerName string         // Consumer Name
	MessageID    MessageID      // 原消息 ID
	Attempt      uint32         // 持久消费次数
	Delivery     BrokerDelivery // 原始 Broker 投递
	Error        string         // 脱敏错误摘要
}

// Broker 可靠消费后端
type BrokerConsumerBackend interface {
	Name() string
	Capabilities(context.Context) (ConsumerCapabilities, error)
	Prepare(context.Context, []Subscription) error
	VisibilityTimeout(context.Context) (time.Duration, error)
	Start(context.Context, BrokerReceiveFunc) (<-chan error, error)
	Stop(context.Context) error
	RenewVisibility(context.Context, Subscription, BrokerDelivery, time.Duration) error
	AdvanceAttempt(context.Context, string, MessageID) (uint32, error)
	ScheduleRetry(context.Context, Subscription, BrokerDelivery, time.Duration) error
	WriteDeadLetter(context.Context, Subscription, BrokerDelivery, uint32, error) error
	Ack(context.Context, Subscription, BrokerDelivery) error
	InspectDeadLetter(context.Context, string, MessageID) (ConsumerDeadLetter, error)
	ReplayDeadLetter(context.Context, string, MessageID, string, string) error
}

// 通用可靠 Broker Consumer Adapter
type BrokerConsumerAdapter struct {
	backend BrokerConsumerBackend
	config  BrokerConsumerConfig

	mutex             sync.Mutex
	isPrepared        bool
	isRunning         bool
	isAccepting       bool
	visibilityTimeout time.Duration
	subscriptions     map[string]Subscription
	deliver           DeliverFunc
	deliveryCtx       context.Context
	stopDeliveries    context.CancelFunc
	inFlight          sync.WaitGroup
}

// 可靠 Broker Consumer Adapter
func NewBrokerConsumerAdapter(
	backend BrokerConsumerBackend,
	config BrokerConsumerConfig,
) (*BrokerConsumerAdapter, error) {
	if backend == nil {
		return nil, gerror.New("outbox consumer adapter: Broker Backend 不能为空")
	}
	if strings.TrimSpace(backend.Name()) == "" {
		return nil, gerror.New("outbox consumer adapter: Adapter Name 不能为空")
	}
	if config.ConsumerTimeout <= 0 || config.MaxEnvelopeBytes <= 0 {
		return nil, gerror.New("outbox consumer adapter: 配置值必须为正数")
	}

	return &BrokerConsumerAdapter{backend: backend, config: config}, nil
}

// Adapter 名称
func (adapter *BrokerConsumerAdapter) Name() string {
	if adapter == nil || adapter.backend == nil {
		return ""
	}

	return adapter.backend.Name()
}

// Broker 可靠消费能力
func (adapter *BrokerConsumerAdapter) Capabilities(ctx context.Context) (ConsumerCapabilities, error) {
	if adapter == nil || adapter.backend == nil {
		return ConsumerCapabilities{}, gerror.New("outbox consumer adapter: Adapter 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return adapter.backend.Capabilities(ctx)
}

// 注册订阅并探测 Broker
func (adapter *BrokerConsumerAdapter) Prepare(
	ctx context.Context,
	subscriptions []Subscription,
	deliver DeliverFunc,
) error {
	if adapter == nil || adapter.backend == nil {
		return gerror.New("outbox consumer adapter: Adapter 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if deliver == nil {
		return gerror.New("outbox consumer adapter: DeliverFunc 不能为空")
	}
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if adapter.isRunning {
		return gerror.New("outbox consumer adapter: Adapter 已启动")
	}
	if adapter.isPrepared {
		return gerror.New("outbox consumer adapter: Adapter 已 Prepare")
	}
	compiled, err := compileSubs(subscriptions)
	if err != nil {
		return err
	}
	capabilities, err := adapter.backend.Capabilities(ctx)
	if err != nil {
		return gerror.Wrap(err, "outbox consumer adapter: 探测 Broker 能力")
	}
	if err = checkCaps(capabilities, adapter.config.MaxEnvelopeBytes); err != nil {
		return err
	}
	visibilityTimeout, err := adapter.backend.VisibilityTimeout(ctx)
	if err != nil {
		return gerror.Wrap(err, "outbox consumer adapter: 探测 Ack Deadline")
	}
	if visibilityTimeout <= 0 {
		return gerror.New("outbox consumer adapter: Ack Deadline 必须为正数")
	}
	if err = adapter.backend.Prepare(ctx, append([]Subscription(nil), subscriptions...)); err != nil {
		return gerror.Wrap(err, "outbox consumer adapter: 验证 Broker 连接和拓扑")
	}

	adapter.isPrepared = true
	adapter.visibilityTimeout = visibilityTimeout
	adapter.subscriptions = compiled
	adapter.deliver = deliver

	return nil
}

// Broker 消费循环
func (adapter *BrokerConsumerAdapter) Start(ctx context.Context) (<-chan error, error) {
	if adapter == nil || adapter.backend == nil {
		return nil, gerror.New("outbox consumer adapter: Adapter 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	adapter.mutex.Lock()
	if !adapter.isPrepared {
		adapter.mutex.Unlock()
		return nil, gerror.New("outbox consumer adapter: Adapter 尚未 Prepare")
	}
	if adapter.isRunning {
		adapter.mutex.Unlock()
		return nil, gerror.New("outbox consumer adapter: Adapter 已启动")
	}
	deliveryCtx, stopDeliveries := context.WithCancel(context.WithoutCancel(ctx))
	adapter.deliveryCtx = deliveryCtx
	adapter.stopDeliveries = stopDeliveries
	adapter.isRunning = true
	adapter.isAccepting = true
	adapter.mutex.Unlock()

	terminated, err := adapter.backend.Start(ctx, adapter.receive)
	if err != nil {
		return nil, errors.Join(
			gerror.Wrap(err, "outbox consumer adapter: 启动 Broker 消费循环"),
			adapter.Stop(ctx),
		)
	}
	if terminated == nil {
		return nil, errors.Join(
			gerror.New("outbox consumer adapter: Broker 终止通道不能为空"),
			adapter.Stop(ctx),
		)
	}

	return terminated, nil
}

// 拉取并排空在途消费
func (adapter *BrokerConsumerAdapter) Stop(ctx context.Context) error {
	if adapter == nil || adapter.backend == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	adapter.mutex.Lock()
	if !adapter.isPrepared {
		adapter.mutex.Unlock()
		return nil
	}
	adapter.isAccepting = false
	isRunning := adapter.isRunning
	stopDeliveries := adapter.stopDeliveries
	adapter.mutex.Unlock()

	stopErr := adapter.backend.Stop(ctx)
	if isRunning {
		drained := make(chan struct{})
		go func() {
			adapter.inFlight.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-ctx.Done():
			if stopDeliveries != nil {
				stopDeliveries()
			}
			stopErr = errors.Join(stopErr, gerror.Wrap(ctx.Err(), "outbox consumer adapter: 等待在途消费"))
		}
	}
	if stopDeliveries != nil {
		stopDeliveries()
	}

	adapter.mutex.Lock()
	adapter.isRunning = false
	adapter.isPrepared = false
	adapter.mutex.Unlock()

	return stopErr
}

// 指定 Consumer 的 DLQ 消息
func (adapter *BrokerConsumerAdapter) InspectDeadLetter(
	ctx context.Context,
	consumerName string,
	messageID MessageID,
) (ConsumerDeadLetter, error) {
	if adapter == nil || adapter.backend == nil {
		return ConsumerDeadLetter{}, gerror.New("outbox consumer adapter: Adapter 不能为空")
	}
	if err := checkConsumerName(consumerName); err != nil {
		return ConsumerDeadLetter{}, err
	}
	if err := checkMessageID(messageID); err != nil {
		return ConsumerDeadLetter{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return adapter.backend.InspectDeadLetter(ctx, consumerName, messageID)
}

// 重新投递 DLQ 消息
func (adapter *BrokerConsumerAdapter) ReplayDeadLetter(
	ctx context.Context,
	consumerName string,
	messageID MessageID,
	operator string,
	reason string,
) error {
	if adapter == nil || adapter.backend == nil {
		return gerror.New("outbox consumer adapter: Adapter 不能为空")
	}
	if err := checkConsumerName(consumerName); err != nil {
		return err
	}
	if err := checkMessageID(messageID); err != nil {
		return err
	}
	if strings.TrimSpace(operator) == "" || strings.TrimSpace(reason) == "" {
		return gerror.New("outbox consumer adapter: Operator 和 Reason 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return adapter.backend.ReplayDeadLetter(ctx, consumerName, messageID, operator, reason)
}

func (adapter *BrokerConsumerAdapter) receive(
	ctx context.Context,
	subscription Subscription,
	delivery BrokerDelivery,
) {
	adapter.mutex.Lock()
	if !adapter.isAccepting {
		adapter.mutex.Unlock()
		return
	}
	adapter.inFlight.Add(1)
	deliveryCtx := adapter.deliveryCtx
	adapter.mutex.Unlock()
	defer adapter.inFlight.Done()

	if ctx == nil {
		ctx = deliveryCtx
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopOnShutdown := context.AfterFunc(deliveryCtx, cancel)
	defer stopOnShutdown()
	renewDone := make(chan struct{})
	go adapter.renewVisibility(ctx, subscription, delivery, cancel, renewDone)
	adapter.handleDelivery(ctx, subscription, delivery)
	cancel()
	<-renewDone
}

func (adapter *BrokerConsumerAdapter) handleDelivery(
	ctx context.Context,
	subscription Subscription,
	delivery BrokerDelivery,
) {
	if delivery == nil {
		return
	}
	registered, err := adapter.findSubscription(subscription)
	if err != nil {
		adapter.saveDeadLetter(ctx, subscription, delivery, 0, err)
		return
	}
	message, err := adapter.restoreDelivery(delivery)
	if err != nil {
		adapter.saveDeadLetter(ctx, registered, delivery, 0, err)
		return
	}
	attempt, err := adapter.backend.AdvanceAttempt(ctx, registered.Name(), message.MessageID())
	if err != nil || attempt == 0 {
		return
	}

	deliveryCtx, cancel := context.WithTimeout(ctx, adapter.config.ConsumerTimeout)
	decision := adapter.deliver(deliveryCtx, registered, message, attempt)
	cancel()
	if ctx.Err() != nil {
		return
	}

	switch decision.Disposition() {
	case DeliveryAck:
		_ = adapter.backend.Ack(ctx, registered, delivery)
	case DeliveryRetry:
		if err = adapter.backend.ScheduleRetry(ctx, registered, delivery, decision.RetryAfter()); err == nil {
			_ = adapter.backend.Ack(ctx, registered, delivery)
		}
	case DeliveryDeadLetter:
		adapter.saveDeadLetter(ctx, registered, delivery, attempt, decision.Error())
	default:
		adapter.saveDeadLetter(
			ctx,
			registered,
			delivery,
			attempt,
			gerror.New("outbox consumer adapter: Delivery Decision 无效"),
		)
	}
}

func (adapter *BrokerConsumerAdapter) saveDeadLetter(
	ctx context.Context,
	subscription Subscription,
	delivery BrokerDelivery,
	attempt uint32,
	cause error,
) {
	if err := adapter.backend.WriteDeadLetter(ctx, subscription, delivery, attempt, cause); err == nil {
		_ = adapter.backend.Ack(ctx, subscription, delivery)
	}
}

func (adapter *BrokerConsumerAdapter) renewVisibility(
	ctx context.Context,
	subscription Subscription,
	delivery BrokerDelivery,
	cancel context.CancelFunc,
	done chan<- struct{},
) {
	defer close(done)
	interval := adapter.visibilityTimeout / 2
	if interval <= 0 {
		interval = adapter.visibilityTimeout
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := adapter.backend.RenewVisibility(ctx, subscription, delivery, adapter.visibilityTimeout); err != nil {
				cancel()
				return
			}
		}
	}
}

func (adapter *BrokerConsumerAdapter) findSubscription(subscription Subscription) (Subscription, error) {
	registered, exists := adapter.subscriptions[subscription.Name()]
	if !exists {
		return Subscription{}, gerror.Newf(
			"outbox consumer adapter: Subscription %q 未注册",
			subscription.Name(),
		)
	}
	if registered.Topic() != subscription.Topic() || registered.MessageType() != subscription.MessageType() ||
		!slices.Equal(registered.SupportedVersions(), subscription.SupportedVersions()) {
		return Subscription{}, gerror.Newf(
			"outbox consumer adapter: Subscription %q 与注册定义不匹配",
			subscription.Name(),
		)
	}

	return registered, nil
}

func (adapter *BrokerConsumerAdapter) restoreDelivery(delivery BrokerDelivery) (Envelope, error) {
	var key *string
	if value, exists := delivery.Key(); exists {
		key = &value
	}
	message, err := Restore(
		delivery.MessageID(),
		delivery.Topic(),
		delivery.MessageType(),
		delivery.Version(),
		key,
		delivery.Payload(),
		delivery.Headers(),
	)
	if err != nil {
		return Envelope{}, gerror.Wrap(err, "outbox consumer adapter: 恢复 Envelope")
	}
	encoded, err := json.Marshal(serializedEnvelope{
		MessageID:   message.MessageID(),
		Topic:       message.Topic(),
		MessageType: message.MessageType(),
		Version:     message.Version(),
		Key:         key,
		Payload:     message.Payload(),
		Headers:     message.Headers(),
	})
	if err != nil {
		return Envelope{}, gerror.Wrap(err, "outbox consumer adapter: 序列化 Envelope")
	}
	if len(encoded) > adapter.config.MaxEnvelopeBytes {
		return Envelope{}, gerror.Newf(
			"outbox consumer adapter: Envelope 超出大小上限: %d > %d",
			len(encoded),
			adapter.config.MaxEnvelopeBytes,
		)
	}

	return message, nil
}

func compileSubs(subscriptions []Subscription) (map[string]Subscription, error) {
	if len(subscriptions) == 0 {
		return nil, gerror.New("outbox consumer adapter: Subscription 不能为空")
	}
	compiled := make(map[string]Subscription, len(subscriptions))
	for _, subscription := range subscriptions {
		if err := checkConsumerName(subscription.Name()); err != nil {
			return nil, err
		}
		if err := checkText("Topic", subscription.Topic()); err != nil {
			return nil, err
		}
		if err := checkText("Message Type", subscription.MessageType()); err != nil {
			return nil, err
		}
		if _, err := versions(subscription.SupportedVersions()); err != nil {
			return nil, err
		}
		if _, exists := compiled[subscription.Name()]; exists {
			return nil, gerror.Newf(
				"outbox consumer adapter: Consumer Name %q 重复",
				subscription.Name(),
			)
		}
		compiled[subscription.Name()] = subscription
	}

	return compiled, nil
}

func checkCaps(capabilities ConsumerCapabilities, envelopeBytes int) error {
	if !capabilities.DurableAck || !capabilities.DurableRetryAttempts || !capabilities.DelayedRetry ||
		!capabilities.DeadLetter || !capabilities.PreservesMessageID {
		return gerror.New("outbox consumer adapter: Broker 缺少可靠消费能力")
	}
	if capabilities.MaxEnvelopeBytes <= 0 {
		return gerror.New("outbox consumer adapter: Broker Envelope 上限必须为正数")
	}
	if capabilities.MaxEnvelopeBytes < envelopeBytes {
		return gerror.Newf(
			"outbox consumer adapter: Broker Envelope 上限不足: %d < %d",
			capabilities.MaxEnvelopeBytes,
			envelopeBytes,
		)
	}

	return nil
}
