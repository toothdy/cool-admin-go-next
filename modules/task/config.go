package task

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
)

// Task 模块运行配置
type Config struct {
	Log LogConfig `json:"log"`
}

// 任务日志保留策略
type LogConfig struct {
	KeepDays int `json:"keepDays"` // 日志保留天数
}

// 任务调度模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "任务调度",
		Description: "任务调度模块，支持分布式任务，由redis整个集群的任务",
		Order:       0,
		Defaults: Config{
			Log: LogConfig{KeepDays: 20},
		},
	}
}
