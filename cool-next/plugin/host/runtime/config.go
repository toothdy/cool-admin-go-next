package runtime

import (
	"fmt"
	"math"
)

const wasmPageBytes = 64 * 1024

// WASM 插件运行时配置
type Config struct {
	MemoryLimitPages uint32
	MaxPayloadBytes  uint32
	DataRoot         string
}

// 返回默认运行时配置
func DefaultConfig() Config {
	return Config{
		MemoryLimitPages: 4096,
		MaxPayloadBytes:  4 * 1024 * 1024,
	}
}

// 校验运行时配置
func (config Config) Validate() error {
	if config.MemoryLimitPages == 0 || config.MemoryLimitPages > 65536 {
		return fmt.Errorf("WASM 内存页上限无效: %d", config.MemoryLimitPages)
	}
	if config.MaxPayloadBytes == 0 {
		return fmt.Errorf("插件载荷上限必须大于 0")
	}
	if config.MaxPayloadBytes > math.MaxInt32 {
		return fmt.Errorf("插件载荷上限不能超过 int32")
	}
	if uint64(config.MaxPayloadBytes) > uint64(config.MemoryLimitPages)*wasmPageBytes {
		return fmt.Errorf("插件载荷上限不能超过 WASM 内存上限")
	}

	return nil
}
