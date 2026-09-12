package gnhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/config"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const (
	DefaultAddress           = "0.0.0.0"                // 默认监听地址
	DefaultPort              = 8001                     // 默认 HTTP 端口
	DefaultStartTimeout      = 5 * time.Second          // 默认启动等待期限
	DefaultClientMaxBodySize = int64(101 * 1024 * 1024) // 默认客户端请求体上限
)

// HTTP Transport 配置
type Config struct {
	Enabled           bool          `json:"enabled"`           // 是否启用 HTTP
	Address           string        `json:"address"`           // 监听地址
	Port              int           `json:"port"`              // 监听端口
	StartTimeout      time.Duration `json:"startTimeout"`      // 启动等待期限
	ClientMaxBodySize int64         `json:"clientMaxBodySize"` // 客户端请求体上限
}

// 返回 HTTP 默认配置
func DefaultConfig() Config {
	return Config{
		Enabled:           true,
		Address:           DefaultAddress,
		Port:              DefaultPort,
		StartTimeout:      DefaultStartTimeout,
		ClientMaxBodySize: DefaultClientMaxBodySize,
	}
}

// 合并并校验 HTTP 配置
func LoadConfig(ctx context.Context, source config.Source) (Config, error) {
	result, err := config.Load(ctx, DefaultConfig(), source)
	if err != nil {
		return Config{}, exception.WrapCore(err, "HTTP Transport 配置无效")
	}
	config := result.Value()
	if err = config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// 校验 HTTP 监听与启动配置
func (config Config) Validate() error {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Address) != config.Address {
		return exception.Core("HTTP Address 无效")
	}
	if config.Port < 0 || config.Port > 65535 {
		return exception.Core(fmt.Sprintf("HTTP Port 必须在 0 到 %d 之间", 65535))
	}
	if config.StartTimeout <= 0 {
		return exception.Core("HTTP StartTimeout 必须大于 0")
	}
	if config.ClientMaxBodySize <= 0 || config.ClientMaxBodySize > DefaultClientMaxBodySize {
		return exception.Core(fmt.Sprintf("HTTP ClientMaxBodySize 必须在 1 到 %d 之间", DefaultClientMaxBodySize))
	}

	return nil
}
