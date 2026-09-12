package recycle

import (
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
)

const cleanupTimeout = 30 * time.Minute

// 回收站模块运行配置
type Config struct {
	Cleanup CleanupConfig `json:"cleanup"`
}

// 过期回收记录清理配置
type CleanupConfig struct {
	Pattern string        `json:"pattern"`
	Timeout time.Duration `json:"timeout"`
}

// 回收站模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "数据回收",
		Description: "收集被删除的数据，管理和恢复",
		Order:       0,
		Defaults: Config{Cleanup: CleanupConfig{
			Pattern: "@daily",
			Timeout: cleanupTimeout,
		}},
	}
}
