package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/outbox/store"
)

const (
	workerIDBytes      = 16
	maxErrorRunes      = 512
	maxCleanupInterval = time.Hour
)

// 发布循环配置
type WorkerConfig struct {
	PollInterval       time.Duration // 轮询间隔
	BatchSize          int           // 单轮领取上限
	LeaseDuration      time.Duration // 单次 Lease 时长
	PublishTimeout     time.Duration // 单次发布时间上限
	PublishMaxAttempts uint32        // 最大发布次数
	PublishRetryBase   time.Duration // 首次重试基数
	PublishRetryMax    time.Duration // 重试等待上限
	Retention          time.Duration // 已发布记录保留期
}

// 发布循环持久化边界
type WorkerStore interface {
	Probe(context.Context) error
	ClaimAvailable(context.Context, string, int, time.Duration) ([]store.Record, error)
	ClaimExpired(context.Context, string, int, time.Duration) ([]store.Record, error)
	Renew(context.Context, string, store.ClaimToken, time.Duration) error
	MarkSent(context.Context, string, store.ClaimToken) error
	MarkRetry(context.Context, string, store.ClaimToken, time.Duration, string) error
	MarkDead(context.Context, string, store.ClaimToken, string) error
	CleanupSent(context.Context, time.Duration, int) (int64, error)
	TopicStatuses(context.Context) ([]store.TopicStatus, error)
}

// 发布状态日志边界
type WorkerLogger interface {
	Info(context.Context, ...any)
	Error(context.Context, ...any)
}

// 发布指标观察边界
type WorkerObserver interface {
	ObserveStatuses(context.Context, []store.TopicStatus)
	ObserveClaim(context.Context, string, bool, time.Duration)
	ObservePublish(context.Context, string, store.Status, uint32, time.Duration)
	ObserveClaimLost(context.Context, string)
}

// 不含 Payload 的结构化日志
type WorkerLog struct {
	Event     string `json:"event"`           // 事件类型
	MessageID string `json:"messageId"`       // 消息 ID
	Topic     string `json:"topic"`           // 消息目的地
	Attempt   uint32 `json:"attempt"`         // 当前发布次数
	WorkerID  string `json:"workerId"`        // Worker 实例 ID
	Error     string `json:"error,omitempty"` // 脱敏错误摘要
}

// Outbox 发布生命周期组件
type Worker struct {
	store     WorkerStore
	publisher Publisher
	config    WorkerConfig
	logger    WorkerLogger
	observer  WorkerObserver
	id        string

	mu            sync.Mutex
	isRunning     bool
	nextExpired   bool
	stopClaims    context.CancelFunc
	stopPublishes context.CancelFunc
	publishCtx    context.Context
	done          chan struct{}
	terminated    chan error
}

type claimedRecord struct {
	record store.Record
}

type noopWorkerObserver struct{}

// 受应用 Host 管理的发布组件
func NewWorker(
	store WorkerStore,
	publisher Publisher,
	config WorkerConfig,
	logger WorkerLogger,
	observer WorkerObserver,
) (*Worker, error) {
	if store == nil || publisher == nil {
		return nil, gerror.New("outbox worker: Store 和 Publisher 不能为空")
	}
	if err := checkWorker(config); err != nil {
		return nil, err
	}
	id := newWorkerID()
	if logger == nil {
		logger = g.Log()
	}
	if observer == nil {
		observer = noopWorkerObserver{}
	}

	return &Worker{
		store:     store,
		publisher: publisher,
		config:    config,
		logger:    logger,
		observer:  observer,
		id:        id,
	}, nil
}

// 当前 Worker 实例 ID
func (worker *Worker) ID() string {
	if worker == nil {
		return ""
	}

	return worker.id
}

// 轮询和 Retention 清理
func (worker *Worker) OnStart(ctx context.Context) error {
	if worker == nil {
		return gerror.New("outbox worker: Worker 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := worker.store.Probe(ctx); err != nil {
		return gerror.Wrap(err, "outbox worker: Store 探测失败")
	}

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.isRunning {
		return gerror.New("outbox worker: Worker 已启动")
	}
	claimCtx, stopClaims := context.WithCancel(context.WithoutCancel(ctx))
	publishCtx, stopPublishes := context.WithCancel(context.WithoutCancel(ctx))
	worker.isRunning = true
	worker.stopClaims = stopClaims
	worker.stopPublishes = stopPublishes
	worker.publishCtx = publishCtx
	worker.done = make(chan struct{})
	worker.terminated = make(chan error, 1)
	go worker.run(claimCtx)

	return nil
}

// 领取并等待在途发布
func (worker *Worker) OnStop(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	worker.mu.Lock()
	if !worker.isRunning {
		worker.mu.Unlock()
		return nil
	}
	stopClaims := worker.stopClaims
	stopPublishes := worker.stopPublishes
	done := worker.done
	worker.mu.Unlock()

	stopClaims()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		stopPublishes()
		return gerror.Wrap(ctx.Err(), "outbox worker: 等待在途发布")
	}
}

// 发布循环终止信号
func (worker *Worker) Terminated() <-chan error {
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.terminated
}

func (worker *Worker) run(ctx context.Context) {
	var runErr error
	defer func() {
		worker.mu.Lock()
		worker.isRunning = false
		worker.stopPublishes()
		close(worker.done)
		if runErr != nil {
			worker.terminated <- runErr
		}
		close(worker.terminated)
		worker.mu.Unlock()
	}()

	cleanupInterval := min(worker.config.Retention, maxCleanupInterval)
	nextCleanup := time.Now()
	for {
		if err := worker.poll(ctx); err != nil && ctx.Err() == nil {
			runErr = gerror.Wrap(err, "outbox worker: 发布轮询异常终止")
			worker.logger.Error(ctx, WorkerLog{Event: "poll_failed", WorkerID: worker.id, Error: errorSummary(runErr)})
			return
		}
		if !time.Now().Before(nextCleanup) {
			if err := worker.cleanup(ctx); err != nil && ctx.Err() == nil {
				runErr = gerror.Wrap(err, "outbox worker: 运维采样异常终止")
				return
			}
			nextCleanup = time.Now().Add(cleanupInterval)
		}

		timer := time.NewTimer(worker.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (worker *Worker) poll(ctx context.Context) error {
	claimed, err := worker.claimBatch(ctx)
	if err != nil {
		return err
	}
	var publishes sync.WaitGroup
	for _, current := range claimed {
		publishes.Add(1)
		go func() {
			defer publishes.Done()
			worker.publish(current)
		}()
	}
	publishes.Wait()

	return nil
}

func (worker *Worker) claimBatch(ctx context.Context) ([]claimedRecord, error) {
	claimExpiredFirst := worker.nextExpired
	worker.nextExpired = !worker.nextExpired
	firstLimit := (worker.config.BatchSize + 1) / 2
	secondLimit := worker.config.BatchSize - firstLimit

	claimed, err := worker.claim(ctx, claimExpiredFirst, firstLimit)
	if err != nil {
		return nil, err
	}
	second, err := worker.claim(ctx, !claimExpiredFirst, worker.config.BatchSize-len(claimed))
	if err != nil {
		return nil, err
	}
	claimed = append(claimed, second...)
	if secondLimit > 0 && len(claimed) < worker.config.BatchSize {
		extra, currentErr := worker.claim(ctx, claimExpiredFirst, worker.config.BatchSize-len(claimed))
		if currentErr != nil {
			return nil, currentErr
		}
		claimed = append(claimed, extra...)
	}

	return claimed, nil
}

func (worker *Worker) claim(ctx context.Context, isExpired bool, limit int) ([]claimedRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	startedAt := time.Now()
	var (
		records []store.Record
		err     error
	)
	if isExpired {
		records, err = worker.store.ClaimExpired(ctx, worker.id, limit, worker.config.LeaseDuration)
	} else {
		records, err = worker.store.ClaimAvailable(ctx, worker.id, limit, worker.config.LeaseDuration)
	}
	if err != nil {
		return nil, gerror.Wrap(err, "outbox worker: 领取消息")
	}
	latency := time.Since(startedAt)
	claimed := make([]claimedRecord, 0, len(records))
	for _, record := range records {
		worker.observer.ObserveClaim(ctx, record.Topic(), isExpired, latency)
		claimed = append(claimed, claimedRecord{record: record})
	}

	return claimed, nil
}

func (worker *Worker) publish(claimed claimedRecord) {
	record := claimed.record
	startedAt := time.Now()
	message, err := envelope(record)
	if err != nil {
		worker.finishFailure(record, startedAt, err)
		return
	}

	publishCtx, cancelPublish := context.WithTimeout(worker.publishCtx, worker.config.PublishTimeout)
	ownership := make(chan error, 1)
	renewDone := make(chan struct{})
	go worker.renew(publishCtx, record, ownership, renewDone)
	err = worker.publisher.Publish(publishCtx, message)
	cancelPublish()
	<-renewDone
	if worker.publishCtx.Err() != nil {
		return
	}
	select {
	case renewErr := <-ownership:
		worker.handleClaimLoss(record, renewErr)
		return
	default:
	}
	if err != nil {
		worker.finishFailure(record, startedAt, err)
		return
	}
	operationCtx, cancelOperation := context.WithTimeout(worker.publishCtx, worker.config.PublishTimeout)
	err = worker.store.MarkSent(operationCtx, record.MessageID(), record.ClaimToken())
	cancelOperation()
	if err != nil {
		worker.handleClaimLoss(record, err)
		return
	}
	worker.observer.ObservePublish(
		worker.publishCtx,
		record.Topic(),
		store.Sent,
		record.Attempts(),
		time.Since(startedAt),
	)
}

func (worker *Worker) renew(
	publishCtx context.Context,
	record store.Record,
	ownership chan<- error,
	done chan<- struct{},
) {
	defer close(done)
	renewInterval := worker.config.LeaseDuration / 2
	if renewInterval <= 0 {
		renewInterval = worker.config.LeaseDuration
	}
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-publishCtx.Done():
			return
		case <-ticker.C:
			renewTimeout := min(worker.config.PublishTimeout, renewInterval)
			renewCtx, cancelRenew := context.WithTimeout(worker.publishCtx, renewTimeout)
			err := worker.store.Renew(
				renewCtx,
				record.MessageID(),
				record.ClaimToken(),
				worker.config.LeaseDuration,
			)
			cancelRenew()
			if err != nil {
				ownership <- err
				return
			}
		}
	}
}

func (worker *Worker) finishFailure(record store.Record, startedAt time.Time, cause error) {
	if worker.publishCtx.Err() != nil {
		return
	}
	summary := errorSummary(cause)
	operationCtx, cancelOperation := context.WithTimeout(worker.publishCtx, worker.config.PublishTimeout)
	defer cancelOperation()
	status := store.Retry
	var err error
	if record.Attempts() >= worker.config.PublishMaxAttempts {
		status = store.Dead
		err = worker.store.MarkDead(operationCtx, record.MessageID(), record.ClaimToken(), summary)
	} else {
		delay, delayErr := retryDelay(worker.config, record.Attempts())
		if delayErr != nil {
			worker.logger.Error(operationCtx, WorkerLog{
				Event:     "retry_jitter_failed",
				MessageID: record.MessageID(),
				Topic:     record.Topic(),
				Attempt:   record.Attempts(),
				WorkerID:  worker.id,
				Error:     errorSummary(delayErr),
			})
			delay = worker.config.PublishRetryMax
		}
		err = worker.store.MarkRetry(operationCtx, record.MessageID(), record.ClaimToken(), delay, summary)
	}
	if err != nil {
		worker.handleClaimLoss(record, err)
		return
	}
	logEntry := WorkerLog{
		Event:     "publish_" + string(status),
		MessageID: record.MessageID(),
		Topic:     record.Topic(),
		Attempt:   record.Attempts(),
		WorkerID:  worker.id,
		Error:     summary,
	}
	if status == store.Dead {
		worker.logger.Error(operationCtx, logEntry)
	} else {
		worker.logger.Info(operationCtx, logEntry)
	}
	worker.observer.ObservePublish(
		operationCtx,
		record.Topic(),
		status,
		record.Attempts(),
		time.Since(startedAt),
	)
}

func (worker *Worker) handleClaimLoss(record store.Record, cause error) {
	if worker.publishCtx.Err() != nil {
		return
	}
	event := "state_update_failed"
	if errors.Is(cause, store.ErrClaimLost) {
		event = "claim_lost"
		worker.observer.ObserveClaimLost(worker.publishCtx, record.Topic())
	}
	worker.logger.Error(worker.publishCtx, WorkerLog{
		Event:     event,
		MessageID: record.MessageID(),
		Topic:     record.Topic(),
		Attempt:   record.Attempts(),
		WorkerID:  worker.id,
		Error:     errorSummary(cause),
	})
}

func (worker *Worker) cleanup(ctx context.Context) error {
	cleaned, err := worker.store.CleanupSent(ctx, worker.config.Retention, worker.config.BatchSize)
	if err != nil {
		if ctx.Err() == nil {
			worker.logger.Error(ctx, WorkerLog{Event: "retention_failed", WorkerID: worker.id, Error: errorSummary(err)})
		}
		return err
	}
	if cleaned > 0 {
		worker.logger.Info(ctx, WorkerLog{Event: "retention_cleaned", WorkerID: worker.id})
	}
	statuses, err := worker.store.TopicStatuses(ctx)
	if err != nil {
		if ctx.Err() == nil {
			worker.logger.Error(ctx, WorkerLog{Event: "status_snapshot_failed", WorkerID: worker.id, Error: errorSummary(err)})
		}
		return err
	}
	worker.observer.ObserveStatuses(ctx, statuses)
	return nil
}

func checkWorker(config WorkerConfig) error {
	if config.PollInterval <= 0 || config.BatchSize <= 0 || config.LeaseDuration <= 0 ||
		config.PublishTimeout <= 0 || config.PublishMaxAttempts == 0 || config.PublishRetryBase <= 0 ||
		config.PublishRetryMax <= 0 || config.Retention <= 0 {
		return gerror.New("outbox worker: 配置值必须为正数")
	}
	if config.PublishTimeout >= config.LeaseDuration {
		return gerror.New("outbox worker: Publish Timeout 必须小于 Lease Duration")
	}
	if config.PublishRetryBase > config.PublishRetryMax {
		return gerror.New("outbox worker: Retry Base 不能大于 Retry Max")
	}

	return nil
}

func retryDelay(config WorkerConfig, attempt uint32) (time.Duration, error) {
	delay := config.PublishRetryBase
	for current := uint32(1); current < attempt && delay < config.PublishRetryMax; current++ {
		if delay > config.PublishRetryMax/2 {
			delay = config.PublishRetryMax
			break
		}
		delay *= 2
	}
	if delay > config.PublishRetryMax {
		delay = config.PublishRetryMax
	}
	floor := delay / 2
	span := delay - floor
	random, err := rand.Int(rand.Reader, big.NewInt(int64(span)+1))
	if err != nil {
		return 0, gerror.Wrap(err, "outbox worker: 生成重试抖动")
	}

	return floor + time.Duration(random.Int64()), nil
}

func newWorkerID() string {
	randomBytes := make([]byte, workerIDBytes)
	rand.Read(randomBytes)

	return hex.EncodeToString(randomBytes)
}

func errorSummary(err error) string {
	if err == nil {
		return ""
	}
	summary := strings.Join(strings.Fields(exception.LogText(err)), " ")
	runes := []rune(summary)
	if len(runes) <= maxErrorRunes {
		return summary
	}

	return string(runes[:maxErrorRunes])
}

// 空实现
func (noopWorkerObserver) ObserveStatuses(context.Context, []store.TopicStatus) {}

// 空实现
func (noopWorkerObserver) ObserveClaim(context.Context, string, bool, time.Duration) {}

// 空实现
func (noopWorkerObserver) ObservePublish(context.Context, string, store.Status, uint32, time.Duration) {
}

// 空实现
func (noopWorkerObserver) ObserveClaimLost(context.Context, string) {}
