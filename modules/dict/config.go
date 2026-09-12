package dict

import "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

// Dict 模块运行配置
type Config struct{}

// 字典模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "字典管理",
		Description: "数据字典等",
		Order:       0,
	}
}
