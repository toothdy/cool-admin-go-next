package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const (
	RedisType     = "redis"         // Redis Store 类型
	MemoryType    = "memory"        // Memory Store 类型
	DefaultGroup  = "default"       // 默认 Redis 连接组
	DefaultPrefix = "cool:session:" // 默认 Redis Key 前缀
)

// Session 后端配置
type SessionConfig struct {
	Type   string `json:"type"`   // Store 类型
	Group  string `json:"group"`  // Redis 连接组
	Prefix string `json:"prefix"` // Redis Key 前缀
}

// 返回默认 Session 后端配置
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{Type: RedisType, Group: DefaultGroup, Prefix: DefaultPrefix}
}

// 按配置创建鉴权 Session Store
func NewSessionStore(ctx context.Context, config SessionConfig) (Store, error) {
	if config == (SessionConfig{}) {
		config = DefaultSessionConfig()
	}
	config.Type = strings.ToLower(strings.TrimSpace(config.Type))
	if config.Type == "" {
		config.Type = RedisType
	}
	switch config.Type {
	case RedisType:
		if config.Group == "" {
			config.Group = DefaultGroup
		}
		if config.Prefix == "" {
			config.Prefix = DefaultPrefix
		}
		return NewRedis(ctx, config.Group, config.Prefix)
	case MemoryType:
		return NewMemory(), nil
	default:
		return nil, exception.Core("Session Store Type 只支持 redis 或 memory")
	}
}

var errExpired = errors.New("session 已过期")

// 校验 Session 快照完整性
func validateSnapshot(value Snapshot, now time.Time) error {
	if !validIdentifier(value.SessionID) {
		return exception.Core("Session ID 无效")
	}
	if !validIdentifier(value.AccessJTI) || !validIdentifier(value.RefreshJTI) {
		return exception.Core("Session JTI 无效")
	}
	if value.ExpiresAt.IsZero() || !value.ExpiresAt.After(now) {
		return exception.Core("Session 已过期")
	}

	return validatePrincipal(Principal{
		Subject:   value.Subject,
		UserID:    value.UserID,
		Username:  value.Username,
		RoleIDs:   value.RoleIDs,
		PasswordV: value.PasswordV,
	})
}

// 校验 Session 标识符
func validIdentifier(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}

	return true
}

// 校验批量撤销身份并生成用户集合
func revokeUserSet(subject Kind, userIDs []uint64) (map[uint64]struct{}, error) {
	if subject != AdminKind && subject != AppKind {
		return nil, exception.Core("Session 用户身份无效")
	}
	targets := make(map[uint64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			return nil, exception.Core("Session 用户身份无效")
		}
		targets[userID] = struct{}{}
	}

	return targets, nil
}

// 返回防御性副本
func cloneSnapshot(value Snapshot) Snapshot {
	value.RoleIDs = append([]uint64(nil), value.RoleIDs...)
	return value
}

const schemaVersion = 1

type wireSession struct {
	SchemaVersion int       `json:"schemaVersion"`
	SessionID     string    `json:"sessionId"`
	Subject       Kind      `json:"subject"`
	UserID        string    `json:"userId"`
	Username      *string   `json:"username,omitempty"`
	RoleIDs       *[]string `json:"roleIds,omitempty"`
	PasswordV     *int      `json:"passwordV,omitempty"`
	AccessJTI     string    `json:"accessJti"`
	RefreshJTI    string    `json:"refreshJti"`
	ExpiresAt     int64     `json:"expiresAt"`
}

// 编码版本化 Session JSON
func encode(value Snapshot, now time.Time) ([]byte, error) {
	if err := validateSnapshot(value, now); err != nil {
		return nil, err
	}

	wire := wireSession{
		SchemaVersion: schemaVersion,
		SessionID:     value.SessionID,
		Subject:       value.Subject,
		UserID:        strconv.FormatUint(value.UserID, 10),
		AccessJTI:     value.AccessJTI,
		RefreshJTI:    value.RefreshJTI,
		ExpiresAt:     value.ExpiresAt.UnixMilli(),
	}
	if value.Subject == AdminKind {
		roleIDs := make([]string, len(value.RoleIDs))
		for index, roleID := range value.RoleIDs {
			roleIDs[index] = strconv.FormatUint(roleID, 10)
		}
		wire.Username = &value.Username
		wire.RoleIDs = &roleIDs
		wire.PasswordV = &value.PasswordV
	}

	content, err := json.Marshal(wire)
	if err != nil {
		return nil, exception.WrapCore(err, "编码 Session 失败")
	}

	return content, nil
}

// 解码并校验版本化 Session JSON
func decode(content []byte, expectedKey, prefix string, now time.Time) (Snapshot, error) {
	if !utf8.Valid(content) {
		return Snapshot{}, exception.Core("Session JSON 不是有效 UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return Snapshot{}, exception.WrapCore(err, "解码 Session 失败")
	}
	var wire wireSession
	if err := json.Unmarshal(content, &wire); err != nil {
		return Snapshot{}, exception.WrapCore(err, "解码 Session 失败")
	}
	if wire.SchemaVersion != schemaVersion {
		return Snapshot{}, exception.Core("Session schemaVersion 不受支持")
	}
	if expectedKey != "" && expectedKey != prefix+wire.SessionID {
		return Snapshot{}, exception.Core("Redis Key 与 Session ID 不一致")
	}

	userID, err := parseUint64("userId", wire.UserID)
	if err != nil {
		return Snapshot{}, err
	}
	value := Snapshot{
		TokenSubject: TokenSubject{
			SessionID: wire.SessionID,
			Subject:   wire.Subject,
			UserID:    userID,
		},
		AccessJTI:  wire.AccessJTI,
		RefreshJTI: wire.RefreshJTI,
		ExpiresAt:  time.UnixMilli(wire.ExpiresAt),
	}

	switch wire.Subject {
	case AdminKind:
		if wire.Username == nil || wire.RoleIDs == nil || wire.PasswordV == nil {
			return Snapshot{}, exception.Core("管理端 Session 字段不完整")
		}
		value.Username = *wire.Username
		value.PasswordV = *wire.PasswordV
		value.RoleIDs = make([]uint64, len(*wire.RoleIDs))
		for index, encodedRoleID := range *wire.RoleIDs {
			roleID, parseErr := parseUint64(fmt.Sprintf("roleIds[%d]", index), encodedRoleID)
			if parseErr != nil {
				return Snapshot{}, parseErr
			}
			value.RoleIDs[index] = roleID
		}
	case AppKind:
		_, hasUsername := fields["username"]
		_, hasRoleIDs := fields["roleIds"]
		_, hasPasswordV := fields["passwordV"]
		if hasUsername || hasRoleIDs || hasPasswordV {
			return Snapshot{}, exception.Core("应用端 Session 不能携带管理端字段")
		}
	default:
		return Snapshot{}, exception.Core("Session 身份种类无效")
	}

	if !value.ExpiresAt.After(now) {
		return Snapshot{}, errExpired
	}
	if err = validateSnapshot(value, now); err != nil {
		return Snapshot{}, err
	}

	return value, nil
}

// 解析十进制 uint64 字段
func parseUint64(field, value string) (uint64, error) {
	if value == "" {
		return 0, exception.Core("Session " + field + " 不能为空")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		if err == nil {
			err = errors.New("不是规范十进制字符串")
		}
		return 0, exception.WrapCore(err, "Session "+field+" 无效")
	}

	return parsed, nil
}

// 并发安全的进程内 Session Store
type MemoryStore struct {
	mutex    sync.Mutex
	sessions map[string]Snapshot
	now      func() time.Time
}

// 创建进程内 Session Store
func NewMemory() *MemoryStore {
	return newMemory(time.Now)
}

// 创建使用指定时钟的进程内 Session Store
func newMemory(now func() time.Time) *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Snapshot), now: now}
}

// 读取有效 Session
func (store *MemoryStore) Get(ctx context.Context, sessionID string) (Snapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, false, err
	}
	if !validIdentifier(sessionID) {
		return Snapshot{}, false, exception.Core("Session ID 无效")
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.cleanupExpired()
	value, exists := store.sessions[sessionID]
	return cloneSnapshot(value), exists, nil
}

// 保存有效 Session
func (store *MemoryStore) Save(ctx context.Context, value Snapshot) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateSnapshot(value, store.now()); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.cleanupExpired()
	store.sessions[value.SessionID] = cloneSnapshot(value)
	return nil
}

// 原子轮换 Access 与 Refresh JTI
func (store *MemoryStore) RotateRefresh(
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
	if err := validateSnapshot(next, store.now()); err != nil {
		return err
	}
	if next.SessionID != sessionID {
		return exception.Core("轮换后的 Session ID 不一致")
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.cleanupExpired()
	current, exists := store.sessions[sessionID]
	if !exists {
		return ErrSessionNotFound
	}
	if current.RefreshJTI != expectedRefreshJTI {
		return ErrRefreshReplay
	}
	if current.Subject != next.Subject || current.UserID != next.UserID {
		return exception.Core("轮换不能改变 Session 身份")
	}
	store.sessions[sessionID] = cloneSnapshot(next)
	return nil
}

// 撤销指定 Session
func (store *MemoryStore) Revoke(ctx context.Context, sessionID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !validIdentifier(sessionID) {
		return exception.Core("Session ID 无效")
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.sessions, sessionID)
	return nil
}

// 按身份种类批量撤销用户 Session
func (store *MemoryStore) RevokeUsers(ctx context.Context, subject Kind, userIDs []uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	targets, err := revokeUserSet(subject, userIDs)
	if err != nil || len(targets) == 0 {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.cleanupExpired()
	for sessionID, value := range store.sessions {
		if _, exists := targets[value.UserID]; value.Subject == subject && exists {
			delete(store.sessions, sessionID)
		}
	}

	return nil
}

// 清理过期 Session
func (store *MemoryStore) cleanupExpired() {
	now := store.now()
	for sessionID, value := range store.sessions {
		if !value.ExpiresAt.After(now) {
			delete(store.sessions, sessionID)
		}
	}
}

// 返回 Context 取消原因
func contextError(ctx context.Context) error {
	if ctx == nil {
		return exception.Core("Session Context 不能为空")
	}

	return ctx.Err()
}
