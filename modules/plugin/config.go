package plugin

import (
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
)

const defaultUploadBytes = 100 << 20 // 100MB

// 插件模块运行配置
type Config struct {
	Upload                UploadConfig  `json:"upload"`
	MaxPackageBytes       int64         `json:"maxPackageBytes"`
	MaxUnpackedBytes      uint64        `json:"maxUnpackedBytes"`
	MaxEntries            int           `json:"maxEntries"`
	MaxPayloadBytes       uint32        `json:"maxPayloadBytes"`
	CallTimeout           time.Duration `json:"callTimeout"`
	InitTimeout           time.Duration `json:"initTimeout"`
	ShutdownTimeout       time.Duration `json:"shutdownTimeout"`
	MemoryLimitPages      uint32        `json:"memoryLimitPages"`
	MaxInstancesPerPlugin int           `json:"maxInstancesPerPlugin"`
	MaxInstances          int           `json:"maxInstances"`
	SyncInterval          time.Duration `json:"syncInterval"`
	MaxCallDepth          int           `json:"maxCallDepth"`
	DataRoot              string        `json:"dataRoot"`
}

// 上传文件的本地存储边界
type UploadConfig struct {
	Root              string   `json:"root"`
	PublicBaseURL     string   `json:"publicBaseURL"`
	MaxBytes          int64    `json:"maxBytes"`
	AllowedExtensions []string `json:"allowedExtensions"`
}

// 返回本地上传默认允许的文件扩展名
func DefaultUploadExts() []string {
	return []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".mp3", ".wav", ".mp4", ".webm"}
}

// 插件模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "插件管理",
		Description: "WASM 插件安装、运行与热切换",
		Order:       0,
		Defaults: Config{
			Upload: UploadConfig{
				Root:              "resource/public/uploads",
				PublicBaseURL:     "http://127.0.0.1:8001",
				MaxBytes:          defaultUploadBytes,
				AllowedExtensions: DefaultUploadExts(),
			},
			MaxPackageBytes:       32 << 20,          // 最大插件包大小 32MB
			MaxUnpackedBytes:      64 << 20,          // 最大解压后大小 64MB
			MaxEntries:            256,               // 最大插件实例数 256个
			MaxPayloadBytes:       4 << 20,           // 最大请求体大小 4MB
			CallTimeout:           30 * time.Second,  // 最大调用超时时间 30秒
			InitTimeout:           15 * time.Second,  // 最大初始化超时时间 15秒
			ShutdownTimeout:       10 * time.Second,  // 最大关闭超时时间 10秒
			MemoryLimitPages:      4096,              // 最大内存限制 4096页
			MaxInstancesPerPlugin: 8,                 // 最大插件实例数 8个
			MaxInstances:          64,                // 最大插件实例数 64个
			SyncInterval:          5 * time.Second,   // 最大同步间隔 5秒
			MaxCallDepth:          8,                 // 最大调用深度 8层
			DataRoot:              "resource/plugin", // 插件数据根目录
		},
	}
}
