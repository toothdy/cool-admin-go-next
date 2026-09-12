package grpc

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/config"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const (
	DefaultAddress = "0.0.0.0" // 默认监听地址
	DefaultPort    = 8002      // 默认 gRPC 端口
	DefaultName    = "cool"    // 默认服务名称
)

// gRPC Transport 配置
type Config struct {
	Enabled  bool   `json:"enabled"`  // 是否启用 gRPC
	Address  string `json:"address"`  // 监听地址
	Port     int    `json:"port"`     // 监听端口
	Name     string `json:"name"`     // 服务发现名称
	Registry bool   `json:"registry"` // 是否注册服务发现
}

// gRPC 默认配置
func DefaultConfig() Config {
	return Config{
		Address: DefaultAddress,
		Port:    DefaultPort,
		Name:    DefaultName,
	}
}

// 合并并校验 gRPC 配置
func LoadConfig(ctx context.Context, source config.Source) (Config, error) {
	result, err := config.Load(ctx, DefaultConfig(), source)
	if err != nil {
		return Config{}, exception.WrapCore(err, "gRPC Transport 配置无效")
	}
	config := result.Value()
	if err = config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// gRPC 监听与服务发现配置
func (config Config) Validate() error {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Address) != config.Address {
		return exception.Core("gRPC Address 无效")
	}
	if config.Port < 0 || config.Port > 65535 {
		return exception.Core(fmt.Sprintf("gRPC Port 必须在 0 到 %d 之间", 65535))
	}
	if strings.TrimSpace(config.Name) == "" || strings.TrimSpace(config.Name) != config.Name {
		return exception.Core("gRPC Name 无效")
	}
	if config.Registry {
		address := net.ParseIP(config.Address)
		if address != nil && address.IsUnspecified() {
			return exception.Core("gRPC Registry 启用时 Address 必须可公开访问")
		}
	}

	return nil
}
