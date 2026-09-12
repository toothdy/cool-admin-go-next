package base

import (
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
)

const (
	defaultCleanupLimit = 30 * time.Minute
)

// 模块运行配置
type Config struct {
	AllowKeys []string     `json:"allowKeys"`
	Log       LogConfig    `json:"log"`
	Coding    CodingConfig `json:"coding"`
}

// 操作日志清理任务
type LogConfig struct {
	CleanupPattern string        `json:"cleanupPattern"`
	CleanupTimeout time.Duration `json:"cleanupTimeout"`
}

// 开发代码工具可访问的项目工作区
type CodingConfig struct {
	Workspace string `json:"workspace"`
}

// 模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "权限管理",
		Description: "基础的权限管理功能，包括登录，权限校验",
		Order:       10,
		GlobalMiddlewares: []module.ComponentRef{
			module.Ref("middleware.NewLogHandler"),
			module.Ref("middleware.NewTranslateHandler"),
		},
		Defaults: Config{
			AllowKeys: []string{},
			Log: LogConfig{
				CleanupPattern: "@daily",
				CleanupTimeout: defaultCleanupLimit,
			},
			Coding: CodingConfig{Workspace: "."},
		},
	}
}
