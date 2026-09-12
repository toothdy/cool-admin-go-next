package auth

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/database/gredis"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const rotateRefreshScript = `
local locator = redis.call('GET', KEYS[1])
if not locator or locator ~= ARGV[1] then return 0 end
local raw = redis.call('HGET', KEYS[2], ARGV[2])
if not raw then return 0 end
if raw ~= ARGV[3] then return 2 end
redis.call('SET', KEYS[1], ARGV[1], 'PXAT', ARGV[5])
redis.call('HSET', KEYS[2], ARGV[2], ARGV[4])
local ttl = redis.call('PTTL', KEYS[2])
if ttl < tonumber(ARGV[6]) then redis.call('PEXPIREAT', KEYS[2], ARGV[5]) end
return 1
`

const saveSessionScript = `
local locator = redis.call('GET', KEYS[1])
if locator and locator ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PXAT', ARGV[4])
redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])
local ttl = redis.call('PTTL', KEYS[2])
if ttl < tonumber(ARGV[5]) then redis.call('PEXPIREAT', KEYS[2], ARGV[4]) end
return 1
`

const revokeSessionScript = `
local locator = redis.call('GET', KEYS[1])
if not locator or locator ~= ARGV[1] then return 0 end
redis.call('HDEL', KEYS[2], ARGV[2])
redis.call('DEL', KEYS[1])
return 1
`

const delSessionScript = `
local raw = redis.call('HGET', KEYS[2], ARGV[2])
if not raw or raw ~= ARGV[3] then return 0 end
local locator = redis.call('GET', KEYS[1])
if locator and locator == ARGV[1] then redis.call('DEL', KEYS[1]) end
redis.call('HDEL', KEYS[2], ARGV[2])
return 1
`

const delLocatorScript = `
local locator = redis.call('GET', KEYS[1])
if not locator or locator ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
return 1
`

const sessionNamespace = "v2:"

type redisBackend interface {
	Do(context.Context, string, ...any) (*gvar.Var, error)
	Get(context.Context, string) (*gvar.Var, error)
	HGet(context.Context, string, string) (*gvar.Var, error)
	Eval(context.Context, string, int64, []string, []any) (*gvar.Var, error)
	Unlink(context.Context, ...string) (int64, error)
}

type redisSessionRecord struct {
	locatorKey   string
	userKey      string
	locatorValue string
	content      []byte
	value        Snapshot
}

// Redis Session Store
type RedisStore struct {
	client redisBackend
	prefix string
	now    func() time.Time
}

// 创建并探测指定连接组的 Redis Session Store
func NewRedis(ctx context.Context, group, prefix string) (*RedisStore, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, exception.Core("Redis Session Group 不能为空")
	}
	if _, exists := gredis.GetConfig(group); !exists {
		return nil, exception.Core("Redis Session Group 不存在: " + group)
	}
	client := gredis.Instance(group)
	if client == nil {
		return nil, exception.Core("创建 Redis Session 连接失败: " + group)
	}
	return newRedisStore(ctx, client, prefix, time.Now)
}

// 创建并探测 Redis Session Store
func newRedisStore(ctx context.Context, client redisBackend, prefix string, now func() time.Time) (*RedisStore, error) {
	if client == nil {
		return nil, exception.Core("Redis Session Client 不能为空")
	}
	if now == nil {
		return nil, exception.Core("Redis Session 时钟不能为空")
	}
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}
	result, err := client.Do(ctx, "PING")
	if err != nil {
		return nil, exception.WrapCore(err, "Redis Session PING 失败")
	}
	if result == nil || !strings.EqualFold(result.String(), "PONG") {
		return nil, exception.Core("Redis Session PING 响应无效")
	}

	return &RedisStore{client: client, prefix: prefix + sessionNamespace, now: now}, nil
}

// 读取有效 Session
func (store *RedisStore) Get(ctx context.Context, sessionID string) (Snapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, false, err
	}
	if !validIdentifier(sessionID) {
		return Snapshot{}, false, exception.Core("Session ID 无效")
	}

	record, exists, err := store.readSession(ctx, sessionID)
	if err != nil || !exists {
		return Snapshot{}, false, err
	}

	return record.value, true, nil
}

// 保存有效 Session
func (store *RedisStore) Save(ctx context.Context, value Snapshot) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	content, err := encode(value, store.now())
	if err != nil {
		return err
	}
	locatorValue := redisSessionLocator(value.Subject, value.UserID)
	result, err := store.client.Eval(
		ctx,
		saveSessionScript,
		2,
		[]string{store.sessionKey(value.SessionID), store.userKey(value.Subject, value.UserID)},
		[]any{locatorValue, value.SessionID, content, value.ExpiresAt.UnixMilli(), value.ExpiresAt.Sub(store.now()).Milliseconds()},
	)
	if err != nil {
		return exception.WrapCore(err, "保存 Redis Session 失败")
	}
	if result == nil || result.Int() != 1 {
		return exception.Core("Redis Session ID 已属于其他用户")
	}

	return nil
}

// 原子轮换 Access 与 Refresh JTI
func (store *RedisStore) RotateRefresh(
	ctx context.Context,
	sessionID string,
	expectedRefreshJTI string,
	next Snapshot,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !validIdentifier(sessionID) || !validIdentifier(expectedRefreshJTI) {
		return exception.Core("Session 轮换参数无效")
	}
	if next.SessionID != sessionID {
		return exception.Core("轮换后的 Session ID 不一致")
	}
	record, exists, err := store.readSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSessionNotFound
	}
	if record.value.RefreshJTI != expectedRefreshJTI {
		return ErrRefreshReplay
	}
	if record.value.Subject != next.Subject || record.value.UserID != next.UserID {
		return exception.Core("轮换不能改变 Session 身份")
	}
	content, err := encode(next, store.now())
	if err != nil {
		return err
	}

	result, err := store.client.Eval(
		ctx,
		rotateRefreshScript,
		2,
		[]string{record.locatorKey, record.userKey},
		[]any{
			record.locatorValue,
			sessionID,
			record.content,
			content,
			next.ExpiresAt.UnixMilli(),
			next.ExpiresAt.Sub(store.now()).Milliseconds(),
		},
	)
	if err != nil {
		return exception.WrapCore(err, "轮换 Redis Session 失败")
	}
	if result == nil {
		return exception.Core("Redis Session 轮换结果无效")
	}
	switch result.Int() {
	case 0:
		return ErrSessionNotFound
	case 1:
		return nil
	case 2:
		return ErrRefreshReplay
	default:
		return exception.Core("Redis Session 轮换结果无效")
	}
}

// 撤销指定 Session
func (store *RedisStore) Revoke(ctx context.Context, sessionID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !validIdentifier(sessionID) {
		return exception.Core("Session ID 无效")
	}
	record, exists, err := store.readSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = store.client.Eval(
		ctx,
		revokeSessionScript,
		2,
		[]string{record.locatorKey, record.userKey},
		[]any{record.locatorValue, sessionID},
	)
	if err != nil {
		return exception.WrapCore(err, "撤销 Redis Session 失败")
	}

	return nil
}

// 按身份种类批量撤销用户 Session
func (store *RedisStore) RevokeUsers(ctx context.Context, subject Kind, userIDs []uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	targets, err := revokeUserSet(subject, userIDs)
	if err != nil || len(targets) == 0 {
		return err
	}

	targetIDs := make([]string, 0, len(targets))
	for userID := range targets {
		targetIDs = append(targetIDs, strconv.FormatUint(userID, 10))
	}
	sort.Strings(targetIDs)
	keys := make([]string, len(targetIDs))
	for index, userID := range targetIDs {
		keys[index] = store.prefix + "user:" + string(subject) + ":" + userID
	}
	if _, err = store.client.Unlink(ctx, keys...); err != nil {
		return exception.WrapCore(err, "撤销 Redis 用户 Session 失败")
	}

	return nil
}

func (store *RedisStore) readSession(ctx context.Context, sessionID string) (redisSessionRecord, bool, error) {
	locatorKey := store.sessionKey(sessionID)
	locatorResult, err := store.client.Get(ctx, locatorKey)
	if err != nil {
		return redisSessionRecord{}, false, exception.WrapCore(err, "读取 Redis Session 定位失败")
	}
	if locatorResult.IsNil() {
		return redisSessionRecord{}, false, nil
	}
	locatorValue := locatorResult.String()
	subject, userID, err := parseLocator(locatorValue)
	if err != nil {
		return redisSessionRecord{}, false, err
	}
	userKey := store.userKey(subject, userID)
	contentResult, err := store.client.HGet(ctx, userKey, sessionID)
	if err != nil {
		return redisSessionRecord{}, false, exception.WrapCore(err, "读取 Redis Session 失败")
	}
	if contentResult.IsNil() {
		if _, err = store.client.Eval(ctx, delLocatorScript, 1, []string{locatorKey}, []any{locatorValue}); err != nil {
			return redisSessionRecord{}, false, exception.WrapCore(err, "清理 Redis Session 定位失败")
		}
		return redisSessionRecord{}, false, nil
	}

	content := contentResult.Bytes()
	value, err := decode(content, "", store.prefix, store.now())
	if errors.Is(err, errExpired) {
		_, cleanupErr := store.client.Eval(
			ctx,
			delSessionScript,
			2,
			[]string{locatorKey, userKey},
			[]any{locatorValue, sessionID, content},
		)
		if cleanupErr != nil {
			return redisSessionRecord{}, false, exception.WrapCore(cleanupErr, "清理过期 Redis Session 失败")
		}
		return redisSessionRecord{}, false, nil
	}
	if err != nil {
		return redisSessionRecord{}, false, err
	}
	if value.SessionID != sessionID || value.Subject != subject || value.UserID != userID {
		return redisSessionRecord{}, false, exception.Core("Redis Session 定位与内容不一致")
	}

	return redisSessionRecord{
		locatorKey:   locatorKey,
		userKey:      userKey,
		locatorValue: locatorValue,
		content:      content,
		value:        value,
	}, true, nil
}

func (store *RedisStore) sessionKey(sessionID string) string {
	return store.prefix + "session:" + sessionID
}

func (store *RedisStore) userKey(subject Kind, userID uint64) string {
	return store.prefix + "user:" + redisSessionLocator(subject, userID)
}

func redisSessionLocator(subject Kind, userID uint64) string {
	return string(subject) + ":" + strconv.FormatUint(userID, 10)
}

func parseLocator(value string) (Kind, uint64, error) {
	subjectValue, userIDValue, found := strings.Cut(value, ":")
	if !found {
		return "", 0, exception.Core("Redis Session 定位无效")
	}
	subject := Kind(subjectValue)
	userID, err := strconv.ParseUint(userIDValue, 10, 64)
	if err != nil || userID == 0 || strconv.FormatUint(userID, 10) != userIDValue ||
		(subject != AdminKind && subject != AppKind) {
		return "", 0, exception.Core("Redis Session 定位无效")
	}

	return subject, userID, nil
}

var _ redisBackend = (*gredis.Redis)(nil)

// 校验 Redis Key 前缀
func validatePrefix(prefix string) error {
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(prefix) != prefix {
		return exception.Core("Redis Session Prefix 无效")
	}
	if strings.ContainsAny(prefix, "*?[]\\") {
		return exception.Core("Redis Session Prefix 不能包含通配符")
	}

	return nil
}
