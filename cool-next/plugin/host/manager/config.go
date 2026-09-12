package manager

import (
	"fmt"
	"math"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
)

const defaultHostVersion = "2.0.0"

// 插件 Manager 配置
type Config struct {
	Runtime               runtime.Config
	ArtifactLimits        bundle.Limits
	HostVersion           string
	CallTimeout           time.Duration
	InitTimeout           time.Duration
	ShutdownTimeout       time.Duration
	SyncInterval          time.Duration
	MaxInstancesPerPlugin int
	MaxInstances          int
	MaxCallDepth          int
}

// 返回插件 Manager 默认配置
func DefaultConfig() Config {
	return Config{
		Runtime:               runtime.DefaultConfig(),
		ArtifactLimits:        bundle.DefaultLimits(),
		HostVersion:           defaultHostVersion,
		CallTimeout:           30 * time.Second,
		InitTimeout:           15 * time.Second,
		ShutdownTimeout:       10 * time.Second,
		SyncInterval:          5 * time.Second,
		MaxInstancesPerPlugin: 8,
		MaxInstances:          64,
		MaxCallDepth:          8,
	}
}

// 校验插件 Manager 配置
func (config Config) Validate() error {
	if err := config.Runtime.Validate(); err != nil {
		return fmt.Errorf("插件 Runtime 配置无效: %w", err)
	}
	if config.ArtifactLimits.MaxPackageBytes <= 0 || config.ArtifactLimits.MaxUnpackedBytes == 0 ||
		config.ArtifactLimits.MaxUnpackedBytes >= math.MaxInt64 || config.ArtifactLimits.MaxEntries <= 0 {
		return fmt.Errorf("插件制品限制无效")
	}
	if config.HostVersion == "" {
		return fmt.Errorf("插件宿主版本不能为空")
	}
	if config.CallTimeout <= 0 || config.InitTimeout <= 0 || config.ShutdownTimeout <= 0 || config.SyncInterval <= 0 {
		return fmt.Errorf("插件调用、初始化、关闭和同步周期必须大于 0")
	}
	if config.MaxInstancesPerPlugin <= 0 || config.MaxInstances <= 0 {
		return fmt.Errorf("插件实例上限必须大于 0")
	}
	if config.MaxInstancesPerPlugin > config.MaxInstances {
		return fmt.Errorf("单插件实例上限不能超过全局实例上限")
	}
	if config.MaxCallDepth <= 0 {
		return fmt.Errorf("插件调用链深度上限必须大于 0")
	}

	return nil
}
