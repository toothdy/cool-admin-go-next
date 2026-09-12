package space

import "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

// 文件空间模块运行配置
type Config struct {
	ShouldDeletePhysicalFile bool `json:"shouldDeletePhysicalFile"` // 是否删除物理文件
}

// 文件空间模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "文件空间",
		Description: "上传和管理文件资源",
		Order:       0,
		Defaults: Config{
			ShouldDeletePhysicalFile: true,
		},
	}
}
