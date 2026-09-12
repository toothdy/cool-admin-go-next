// Package session 提供鉴权 Session 后端的兼容入口
package session

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
)

const (
	RedisType     = auth.RedisType     // Redis 后端类型
	MemoryType    = auth.MemoryType    // 进程内后端类型
	DefaultGroup  = auth.DefaultGroup  // 默认 Redis 连接组
	DefaultPrefix = auth.DefaultPrefix // 默认 Session 键前缀
)

var (
	ErrNotFound      = auth.ErrSessionNotFound // Session 不存在
	ErrRefreshReplay = auth.ErrRefreshReplay   // Refresh Token 重放
)

// Session 后端配置兼容别名
type Config = auth.SessionConfig

// Session 存储兼容别名
type Store = auth.Store

// 进程内 Session 存储兼容别名
type MemoryStore = auth.MemoryStore

// Redis Session 存储兼容别名
type RedisStore = auth.RedisStore

// 返回默认 Redis 配置
func DefaultConfig() Config {
	return auth.DefaultSessionConfig()
}

// 按显式配置创建 Session Store
func New(ctx context.Context, config Config) (Store, error) {
	return auth.NewSessionStore(ctx, config)
}

// 创建进程内 Session Store
func NewMemory() *MemoryStore {
	return auth.NewMemory()
}

// 创建并探测指定连接组的 Redis Session Store
func NewRedis(ctx context.Context, group, prefix string) (*RedisStore, error) {
	return auth.NewRedis(ctx, group, prefix)
}
