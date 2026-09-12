package outbox

import (
	"math"
	"strings"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
)

const metadataAllowance = 64 * 1024

// 可靠消息基础设施配置
type Config struct {
	Enabled             bool          `json:"enabled"`             // 是否启用可靠消息
	DatabaseGroup       string        `json:"databaseGroup"`       // Framework Database Group
	PollInterval        time.Duration `json:"pollInterval"`        // 发布轮询间隔
	BatchSize           int           `json:"batchSize"`           // 单轮领取上限
	LeaseDuration       time.Duration `json:"leaseDuration"`       // 单次 Lease 时长
	PublishTimeout      time.Duration `json:"publishTimeout"`      // 单次发布时间上限
	PublishMaxAttempts  uint32        `json:"publishMaxAttempts"`  // 最大发布次数
	PublishRetryBase    time.Duration `json:"publishRetryBase"`    // 发布首次重试基数
	PublishRetryMax     time.Duration `json:"publishRetryMax"`     // 发布重试等待上限
	ConsumerTimeout     time.Duration `json:"consumerTimeout"`     // 单次消费时间上限
	ConsumerMaxAttempts uint32        `json:"consumerMaxAttempts"` // 最大消费次数
	ConsumerRetryBase   time.Duration `json:"consumerRetryBase"`   // 消费首次重试基数
	ConsumerRetryMax    time.Duration `json:"consumerRetryMax"`    // 消费重试等待上限
	Retention           time.Duration `json:"retention"`           // 已发布记录保留期
	MaxPayloadBytes     int           `json:"maxPayloadBytes"`     // 消息载荷上限
	MaxHeaderBytes      int           `json:"maxHeaderBytes"`      // 消息 Header 上限
}

// 设计约定的默认配置
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		DatabaseGroup:       "default",
		PollInterval:        500 * time.Millisecond,
		BatchSize:           100,
		LeaseDuration:       30 * time.Second,
		PublishTimeout:      10 * time.Second,
		PublishMaxAttempts:  12,
		PublishRetryBase:    time.Second,
		PublishRetryMax:     5 * time.Minute,
		ConsumerTimeout:     30 * time.Second,
		ConsumerMaxAttempts: 12,
		ConsumerRetryBase:   time.Second,
		ConsumerRetryMax:    5 * time.Minute,
		Retention:           168 * time.Hour,
		MaxPayloadBytes:     1_048_576,
		MaxHeaderBytes:      16_384,
	}
}

// 可靠消息基础设施配置
func (config Config) Validate() error {
	if strings.TrimSpace(config.DatabaseGroup) == "" || strings.TrimSpace(config.DatabaseGroup) != config.DatabaseGroup {
		return gerror.New("outbox config: Database Group 无效")
	}
	if config.PollInterval <= 0 || config.BatchSize <= 0 || config.LeaseDuration <= 0 ||
		config.PublishTimeout <= 0 || config.PublishMaxAttempts == 0 || config.PublishRetryBase <= 0 ||
		config.PublishRetryMax <= 0 || config.ConsumerTimeout <= 0 || config.ConsumerMaxAttempts == 0 ||
		config.ConsumerRetryBase <= 0 || config.ConsumerRetryMax <= 0 || config.Retention <= 0 ||
		config.MaxPayloadBytes <= 0 || config.MaxHeaderBytes <= 0 {
		return gerror.New("outbox config: 批量、重试、Timeout、Retention 和大小参数必须为正数")
	}
	if config.PublishTimeout >= config.LeaseDuration {
		return gerror.New("outbox config: Publish Timeout 必须小于 Lease Duration")
	}
	if config.PublishRetryBase > config.PublishRetryMax {
		return gerror.New("outbox config: Publish Retry Base 不能大于 Retry Max")
	}
	if config.ConsumerRetryBase > config.ConsumerRetryMax {
		return gerror.New("outbox config: Consumer Retry Base 不能大于 Retry Max")
	}
	if config.MaxPayloadBytes > math.MaxInt-config.MaxHeaderBytes-metadataAllowance {
		return gerror.New("outbox config: 消息大小参数超出整数范围")
	}

	return nil
}

// 完整 Envelope 上限
func (config Config) MaxEnvelopeBytes() int {
	return config.MaxPayloadBytes + config.MaxHeaderBytes + metadataAllowance
}

// 发布循环配置
func (config Config) WorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval:       config.PollInterval,
		BatchSize:          config.BatchSize,
		LeaseDuration:      config.LeaseDuration,
		PublishTimeout:     config.PublishTimeout,
		PublishMaxAttempts: config.PublishMaxAttempts,
		PublishRetryBase:   config.PublishRetryBase,
		PublishRetryMax:    config.PublishRetryMax,
		Retention:          config.Retention,
	}
}

// 消费事务配置
func (config Config) ConsumerConfig() ConsumerConfig {
	return ConsumerConfig{
		ConsumerTimeout:     config.ConsumerTimeout,
		ConsumerMaxAttempts: config.ConsumerMaxAttempts,
		ConsumerRetryBase:   config.ConsumerRetryBase,
		ConsumerRetryMax:    config.ConsumerRetryMax,
	}
}

// 入队大小限制
func (config Config) EnqueueLimits() EnqueueLimits {
	return EnqueueLimits{
		MaxPayloadBytes:  config.MaxPayloadBytes,
		MaxHeaderBytes:   config.MaxHeaderBytes,
		MaxEnvelopeBytes: config.MaxEnvelopeBytes(),
	}
}

// Broker Consumer Adapter 配置
func (config Config) BrokerConsumerConfig() BrokerConsumerConfig {
	return BrokerConsumerConfig{
		ConsumerTimeout:  config.ConsumerTimeout,
		MaxEnvelopeBytes: config.MaxEnvelopeBytes(),
	}
}
