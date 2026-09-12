package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/errors/gerror"

	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
)

// 三数据库可靠消息存储
type DatabaseStore struct {
	runtime    *db.Runtime
	statements sqlStatements
}

type storedRecord struct {
	MessageID      string     `orm:"messageId"`
	Topic          string     `orm:"topic"`
	MessageType    string     `orm:"messageType"`
	MessageVersion uint32     `orm:"messageVersion"`
	MessageKey     *string    `orm:"messageKey"`
	Payload        []byte     `orm:"payload"`
	Headers        []byte     `orm:"headers"`
	Status         Status     `orm:"status"`
	Attempts       uint32     `orm:"attempts"`
	AvailableAt    time.Time  `orm:"availableAt"`
	LeaseOwner     *string    `orm:"leaseOwner"`
	ClaimToken     *string    `orm:"claimToken"`
	LeaseExpiresAt *time.Time `orm:"leaseExpiresAt"`
	LastError      *string    `orm:"lastError"`
	CreateTime     time.Time  `orm:"createTime"`
	UpdateTime     time.Time  `orm:"updateTime"`
	SentAt         *time.Time `orm:"sentAt"`
}

type claimCandidate struct {
	MessageID string `orm:"messageId"`
}

type storedTopicStatus struct {
	Topic        string    `orm:"topic"`
	Status       Status    `orm:"status"`
	Count        int64     `orm:"count"`
	OldestTime   time.Time `orm:"oldestTime"`
	DatabaseTime time.Time `orm:"databaseTime"`
}

type storedMetadata struct {
	MessageID      string     `orm:"messageId"`
	Topic          string     `orm:"topic"`
	MessageType    string     `orm:"messageType"`
	MessageVersion uint32     `orm:"messageVersion"`
	Status         Status     `orm:"status"`
	Attempts       uint32     `orm:"attempts"`
	AvailableAt    time.Time  `orm:"availableAt"`
	LeaseExpiresAt *time.Time `orm:"leaseExpiresAt"`
	LastError      *string    `orm:"lastError"`
	CreateTime     time.Time  `orm:"createTime"`
	UpdateTime     time.Time  `orm:"updateTime"`
	SentAt         *time.Time `orm:"sentAt"`
}

type sqliteErrorCoder interface {
	Code() int
}

// 当前 Framework Database Group 的 Store
func New(runtime *db.Runtime) (*DatabaseStore, error) {
	if runtime.DB() == nil || runtime.Group() == "" {
		return nil, gerror.New("outbox store: 数据库 Runtime 无效")
	}
	statements, err := newSQLStatements(runtime.Dialect())
	if err != nil {
		return nil, err
	}

	return &DatabaseStore{runtime: runtime, statements: statements}, nil
}

// 初始待发布记录
func (store *DatabaseStore) Enqueue(ctx context.Context, transaction gdb.TX, record Record) error {
	if store == nil || store.runtime == nil || transaction == nil || transaction.GetDB() == nil {
		return gerror.New("outbox store: 入队事务无效")
	}
	if transaction.GetDB().GetGroup() != store.runtime.Group() {
		return gerror.Newf(
			"outbox store: 入队事务组不匹配，期望 %s，实际 %s",
			store.runtime.Group(),
			transaction.GetDB().GetGroup(),
		)
	}
	_, err := transaction.Ctx(ctx).Exec(
		store.statements.enqueue,
		record.messageID,
		record.topic,
		record.messageType,
		record.messageVersion,
		record.messageKey,
		record.payload,
		record.headers,
	)
	if err != nil {
		return gerror.Wrap(err, "outbox store: 写入待发布记录")
	}

	return nil
}

// 抢占可发布记录的所有权
func (store *DatabaseStore) Claim(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]Record, error) {
	return store.claim(ctx, owner, limit, leaseDuration, store.statements.claimCandidates, store.statements.claim)
}

// 抢占可立即发布的记录
func (store *DatabaseStore) ClaimAvailable(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]Record, error) {
	return store.claim(
		ctx,
		owner,
		limit,
		leaseDuration,
		store.statements.claimReadySQL,
		store.statements.claimAvailable,
	)
}

// 抢占 Lease 已过期的记录
func (store *DatabaseStore) ClaimExpired(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]Record, error) {
	return store.claim(
		ctx,
		owner,
		limit,
		leaseDuration,
		store.statements.claimExpiredSQL,
		store.statements.claimExpired,
	)
}

func (store *DatabaseStore) claim(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
	candidatesStatement string,
	claimStatement string,
) ([]Record, error) {
	if store == nil || store.runtime == nil {
		return nil, gerror.New("outbox store: Store 未初始化")
	}
	if strings.TrimSpace(owner) == "" || limit <= 0 || leaseDuration <= 0 {
		return nil, gerror.New("outbox store: Claim 参数无效")
	}
	claim := func(transactionCtx context.Context, transaction gdb.TX) ([]Record, error) {
		current, err := store.claimWithin(
			transactionCtx,
			transaction,
			owner,
			limit,
			leaseDuration,
			candidatesStatement,
			claimStatement,
		)
		if err != nil {
			return nil, err
		}
		return current, nil
	}
	if store.runtime.Dialect().Kind() == driver.SQLite {
		return store.claimSQLite(ctx, claim)
	}
	var records []Record
	options := gdb.DefaultTxOptions()
	options.Isolation = sql.LevelReadCommitted
	if err := store.runtime.DB().TransactionWithOptions(ctx, options, func(transactionCtx context.Context, transaction gdb.TX) error {
		current, err := claim(transactionCtx, transaction)
		records = current
		return err
	}); err != nil {
		return nil, gerror.Wrap(err, "outbox store: READ COMMITTED 领取事务")
	}

	return records, nil
}

func (store *DatabaseStore) claimSQLite(
	ctx context.Context,
	claim func(context.Context, gdb.TX) ([]Record, error),
) ([]Record, error) {
	const maxAttempts = 8
	for attempt := range maxAttempts {
		var records []Record
		err := store.runtime.DB().Transaction(ctx, func(transactionCtx context.Context, transaction gdb.TX) error {
			current, claimErr := claim(transactionCtx, transaction)
			records = current
			return claimErr
		})
		if err == nil {
			return records, nil
		}
		if !isSQLiteBusy(err) || attempt == maxAttempts-1 {
			return nil, gerror.Wrap(err, "outbox store: SQLite 领取事务")
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, gerror.Wrap(ctx.Err(), "outbox store: 等待 SQLite 领取重试")
		case <-timer.C:
		}
	}

	return nil, gerror.New("outbox store: SQLite 领取重试耗尽")
}

func (store *DatabaseStore) claimWithin(
	ctx context.Context,
	transaction gdb.TX,
	owner string,
	limit int,
	leaseDuration time.Duration,
	candidatesStatement string,
	claimStatement string,
) ([]Record, error) {
	candidates := make([]claimCandidate, 0, limit)
	if err := transaction.Ctx(ctx).GetScan(&candidates, candidatesStatement, limit); err != nil {
		return nil, gerror.Wrap(err, "outbox store: 读取领取候选")
	}
	records := make([]Record, 0, len(candidates))
	for _, candidate := range candidates {
		token := newClaimToken()
		arguments := store.statements.claimArguments(owner, token, leaseDuration, candidate.MessageID)
		result, err := transaction.Ctx(ctx).Exec(claimStatement, arguments...)
		if err != nil {
			return nil, gerror.Wrapf(err, "outbox store: 领取消息 %s", candidate.MessageID)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, gerror.Wrap(err, "outbox store: 读取领取影响行数")
		}
		if affected == 0 && store.runtime.Dialect().Kind() == driver.SQLite {
			continue
		}
		if affected != 1 {
			return nil, invariantError("领取", affected)
		}
		record, err := store.readClaimed(ctx, transaction, candidate.MessageID, token)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, nil
}

func (store *DatabaseStore) readClaimed(
	ctx context.Context,
	transaction gdb.TX,
	messageID string,
	token ClaimToken,
) (Record, error) {
	var stored storedRecord
	if err := transaction.Ctx(ctx).GetScan(&stored, store.statements.readClaimed, messageID, token); err != nil {
		return Record{}, gerror.Wrap(err, "outbox store: 回读已领取记录")
	}
	if stored.MessageID == "" {
		return Record{}, gerror.New("outbox store: Claim Token 回读未命中")
	}

	return stored.toRecord()
}

// 续签当前 Lease 截止
func (store *DatabaseStore) Renew(
	ctx context.Context,
	messageID string,
	token ClaimToken,
	leaseDuration time.Duration,
) error {
	if leaseDuration <= 0 {
		return gerror.New("outbox store: Lease Duration 必须为正数")
	}
	arguments := store.statements.renewArguments(leaseDuration, messageID, token)
	return store.execClaimUpdate(ctx, "续租", store.statements.renew, arguments...)
}

// 将记录置为已发布
func (store *DatabaseStore) MarkSent(ctx context.Context, messageID string, token ClaimToken) error {
	return store.execClaimUpdate(ctx, "标记已发布", store.statements.markSent, messageID, token)
}

// 推迟记录至下次可领取
func (store *DatabaseStore) MarkRetry(
	ctx context.Context,
	messageID string,
	token ClaimToken,
	retryAfter time.Duration,
	summary string,
) error {
	if retryAfter < 0 {
		return gerror.New("outbox store: Retry Delay 不能为负数")
	}
	arguments := store.statements.retryArguments(retryAfter, summary, messageID, token)
	return store.execClaimUpdate(ctx, "标记重试", store.statements.markRetry, arguments...)
}

// 将记录置为死信
func (store *DatabaseStore) MarkDead(
	ctx context.Context,
	messageID string,
	token ClaimToken,
	summary string,
) error {
	return store.execClaimUpdate(ctx, "标记死信", store.statements.markDead, summary, messageID, token)
}

// 超过保留期的已发布记录
func (store *DatabaseStore) CleanupSent(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	if store == nil || store.runtime == nil {
		return 0, gerror.New("outbox store: Store 未初始化")
	}
	if retention <= 0 || limit <= 0 {
		return 0, gerror.New("outbox store: Retention 清理参数无效")
	}
	var cleaned int64
	err := store.runtime.DB().Transaction(ctx, func(transactionCtx context.Context, transaction gdb.TX) error {
		candidates := make([]claimCandidate, 0, limit)
		threshold := store.statements.durationArgument(-retention)
		if err := transaction.Ctx(transactionCtx).GetScan(
			&candidates,
			store.statements.cleanupCandidates,
			threshold,
			limit,
		); err != nil {
			return gerror.Wrap(err, "outbox store: 读取 Retention 清理候选")
		}
		for _, candidate := range candidates {
			result, err := transaction.Ctx(transactionCtx).Exec(
				store.statements.cleanupSent,
				candidate.MessageID,
				threshold,
			)
			if err != nil {
				return gerror.Wrapf(err, "outbox store: 清理已发布消息 %s", candidate.MessageID)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return gerror.Wrap(err, "outbox store: 读取 Retention 清理影响行数")
			}
			if affected != 0 && affected != 1 {
				return invariantError("清理已发布消息", affected)
			}
			cleaned += affected
		}
		return nil
	})
	if err != nil {
		return 0, gerror.Wrap(err, "outbox store: Retention 清理事务")
	}

	return cleaned, nil
}

// 按 Topic 和状态聚合的可观测快照
func (store *DatabaseStore) TopicStatuses(ctx context.Context) ([]TopicStatus, error) {
	if store == nil || store.runtime == nil {
		return nil, gerror.New("outbox store: Store 未初始化")
	}
	var stored []storedTopicStatus
	if err := store.runtime.DB().GetScan(ctx, &stored, store.statements.topicStatuses); err != nil {
		return nil, gerror.Wrap(err, "outbox store: 读取状态快照")
	}
	statuses := make([]TopicStatus, 0, len(stored))
	for _, current := range stored {
		if !validStatus(current.Status) {
			return nil, gerror.Newf("outbox store: 状态快照包含未知状态 %q", current.Status)
		}
		oldestAge := current.DatabaseTime.Sub(current.OldestTime)
		if oldestAge < 0 {
			oldestAge = 0
		}
		statuses = append(statuses, TopicStatus{
			Topic:     current.Topic,
			Status:    current.Status,
			Count:     current.Count,
			OldestAge: oldestAge,
		})
	}

	return statuses, nil
}

// 指定状态的安全运维快照
func (store *DatabaseStore) List(ctx context.Context, filter ListFilter) ([]Metadata, error) {
	if store == nil || store.runtime == nil {
		return nil, gerror.New("outbox store: Store 未初始化")
	}
	topic := strings.TrimSpace(filter.Topic)
	if !validStatus(filter.Status) || filter.Limit <= 0 || filter.Limit > MaxListLimit ||
		(filter.Topic != "" && topic == "") {
		return nil, gerror.New("outbox store: 运维列表参数无效")
	}
	stored := make([]storedMetadata, 0, filter.Limit)
	var err error
	if topic == "" {
		err = store.runtime.DB().GetScan(ctx, &stored, store.statements.list, filter.Status, filter.Limit)
	} else {
		err = store.runtime.DB().GetScan(ctx, &stored, store.statements.listByTopic, filter.Status, topic, filter.Limit)
	}
	if err != nil {
		return nil, gerror.Wrap(err, "outbox store: 查询运维列表")
	}
	result := make([]Metadata, 0, len(stored))
	for _, current := range stored {
		metadata, convertErr := current.toMetadata()
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, metadata)
	}

	return result, nil
}

// 单条安全运维快照
func (store *DatabaseStore) Show(ctx context.Context, messageID string) (Metadata, error) {
	if store == nil || store.runtime == nil {
		return Metadata{}, gerror.New("outbox store: Store 未初始化")
	}
	if !validMessageID(messageID) {
		return Metadata{}, gerror.New("outbox store: Message ID 无效")
	}
	var stored storedMetadata
	if err := store.runtime.DB().GetScan(ctx, &stored, store.statements.show, messageID); errors.Is(err, sql.ErrNoRows) {
		return Metadata{}, ErrNotFound
	} else if err != nil {
		return Metadata{}, gerror.Wrap(err, "outbox store: 查询运维详情")
	}
	if stored.MessageID == "" {
		return Metadata{}, ErrNotFound
	}

	return stored.toMetadata()
}

func (store *DatabaseStore) execClaimUpdate(
	ctx context.Context,
	action string,
	statement string,
	arguments ...any,
) error {
	if store == nil || store.runtime == nil {
		return gerror.New("outbox store: Store 未初始化")
	}
	result, err := store.runtime.DB().Exec(ctx, statement, arguments...)
	if err != nil {
		return gerror.Wrapf(err, "outbox store: %s", action)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return gerror.Wrapf(err, "outbox store: 读取%s影响行数", action)
	}
	if affected == 0 {
		return gerror.Wrap(ErrClaimLost, action)
	}
	if affected != 1 {
		return invariantError(action, affected)
	}

	return nil
}

// 将死信回退为待重试
func (store *DatabaseStore) ReplayDead(ctx context.Context, messageID string) error {
	if store == nil || store.runtime == nil {
		return gerror.New("outbox store: Store 未初始化")
	}
	if !validMessageID(messageID) {
		return gerror.New("outbox store: Message ID 无效")
	}
	err := store.runtime.DB().Transaction(ctx, func(transactionCtx context.Context, transaction gdb.TX) error {
		var current storedMetadata
		if readErr := transaction.Ctx(transactionCtx).GetScan(
			&current,
			store.statements.lockForReplay,
			messageID,
		); readErr != nil {
			if errors.Is(readErr, sql.ErrNoRows) {
				return ErrNotFound
			}
			return gerror.Wrap(readErr, "outbox store: 锁定死信")
		}
		if current.MessageID == "" {
			return ErrNotFound
		}
		if current.Status != Dead {
			return ErrReplayRejected
		}
		result, updateErr := transaction.Ctx(transactionCtx).Exec(store.statements.replayDead, messageID)
		if updateErr != nil {
			return gerror.Wrap(updateErr, "outbox store: 重放死信")
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return gerror.Wrap(rowsErr, "outbox store: 读取死信重放影响行数")
		}
		if affected == 0 {
			return ErrReplayConflict
		}
		if affected != 1 {
			return invariantError("重放死信", affected)
		}

		return nil
	})
	if err != nil {
		return gerror.Wrap(err, "outbox store: 死信重放事务")
	}

	return nil
}

// 消费幂等标记
func (store *DatabaseStore) InsertIfAbsent(
	ctx context.Context,
	transaction gdb.TX,
	consumer string,
	messageID string,
) (bool, error) {
	if transaction == nil || strings.TrimSpace(consumer) == "" || !validMessageID(messageID) {
		return false, gerror.New("outbox store: Inbox 参数无效")
	}
	if transaction.GetDB() == nil || transaction.GetDB().GetGroup() != store.runtime.Group() {
		return false, gerror.New("outbox store: Inbox 事务组不匹配")
	}
	switch store.runtime.Dialect().Kind() {
	case driver.MySQL:
		_, err := transaction.Ctx(ctx).Exec(store.statements.insertInbox, consumer, messageID)
		if err == nil {
			return true, nil
		}
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			return false, nil
		}
		return false, gerror.Wrap(err, "outbox store: 写入 MySQL Inbox")
	case driver.PostgreSQL:
		record, err := transaction.Ctx(ctx).GetOne(store.statements.insertInbox, consumer, messageID)
		if err != nil {
			return false, gerror.Wrap(err, "outbox store: 写入 PostgreSQL Inbox")
		}
		return !record.IsEmpty(), nil
	case driver.SQLite:
		result, err := transaction.Ctx(ctx).Exec(store.statements.insertInbox, consumer, messageID)
		if err != nil {
			return false, gerror.Wrap(err, "outbox store: 写入 SQLite Inbox")
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, gerror.Wrap(err, "outbox store: 读取 SQLite Inbox 影响行数")
		}
		if affected != 0 && affected != 1 {
			return false, invariantError("写入 Inbox", affected)
		}
		return affected == 1, nil
	default:
		return false, gerror.Newf("outbox store: 不支持的数据库类型 %s", store.runtime.Dialect().Kind())
	}
}

func (stored storedRecord) toRecord() (Record, error) {
	if !validMessageID(stored.MessageID) {
		return Record{}, gerror.New("outbox store: 数据库返回的 Message ID 无效")
	}
	if !validStatus(stored.Status) {
		return Record{}, gerror.Newf("outbox store: 数据库返回未知状态 %q", stored.Status)
	}
	claimToken := ClaimToken("")
	if stored.ClaimToken != nil {
		claimToken = ClaimToken(*stored.ClaimToken)
	}

	return Record{
		messageID:      stored.MessageID,
		topic:          stored.Topic,
		messageType:    stored.MessageType,
		messageVersion: stored.MessageVersion,
		messageKey:     cloneString(stored.MessageKey),
		payload:        append([]byte(nil), stored.Payload...),
		headers:        append([]byte(nil), stored.Headers...),
		status:         stored.Status,
		attempts:       stored.Attempts,
		availableAt:    stored.AvailableAt,
		leaseOwner:     cloneString(stored.LeaseOwner),
		claimToken:     claimToken,
		leaseExpiresAt: cloneTime(stored.LeaseExpiresAt),
		lastError:      cloneString(stored.LastError),
		createTime:     stored.CreateTime,
		updateTime:     stored.UpdateTime,
		sentAt:         cloneTime(stored.SentAt),
	}, nil
}

func (stored storedMetadata) toMetadata() (Metadata, error) {
	if !validMessageID(stored.MessageID) {
		return Metadata{}, gerror.New("outbox store: 数据库返回的 Message ID 无效")
	}
	if !validStatus(stored.Status) {
		return Metadata{}, gerror.Newf("outbox store: 数据库返回未知状态 %q", stored.Status)
	}

	return Metadata{
		MessageID:      stored.MessageID,
		Topic:          stored.Topic,
		MessageType:    stored.MessageType,
		MessageVersion: stored.MessageVersion,
		Status:         stored.Status,
		Attempts:       stored.Attempts,
		AvailableAt:    stored.AvailableAt,
		LeaseExpiresAt: cloneTime(stored.LeaseExpiresAt),
		LastError:      cloneString(stored.LastError),
		CreateTime:     stored.CreateTime,
		UpdateTime:     stored.UpdateTime,
		SentAt:         cloneTime(stored.SentAt),
	}, nil
}

func validStatus(status Status) bool {
	return status == Pending || status == Retry || status == Leased || status == Sent || status == Dead
}

func newClaimToken() ClaimToken {
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)

	return ClaimToken(hex.EncodeToString(randomBytes))
}

func invariantError(action string, affected int64) error {
	return gerror.Newf("outbox store: %s破坏存储不变量，影响行数为 %d", action, affected)
}

func isSQLiteBusy(err error) bool {
	var sqliteError sqliteErrorCoder
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == 5
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

var _ Store = (*DatabaseStore)(nil)
