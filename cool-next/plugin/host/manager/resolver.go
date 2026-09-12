package manager

import (
	"fmt"
	"sync"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 启用插件 key 与 hook 的原子索引
type resolver struct {
	mu     sync.RWMutex
	byKey  map[string]*generation
	byHook map[string]*generation
}

// 创建空目标解析器
func newResolver() *resolver {
	return &resolver{
		byKey:  make(map[string]*generation),
		byHook: make(map[string]*generation),
	}
}

// 发布新 generation 并返回被替换的旧版本
func (resolver *resolver) publish(next *generation) (*generation, error) {
	if next == nil || next.key() == "" {
		return nil, fmt.Errorf("插件 generation 无效")
	}
	key, hook := next.key(), next.hook()

	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if conflict := resolver.byHook[key]; conflict != nil && conflict.key() != key {
		return nil, fmt.Errorf("插件 key %q 与插件 %q 的 hook 冲突", key, conflict.key())
	}
	if hook != "" {
		if conflict := resolver.byKey[hook]; conflict != nil && conflict.key() != key {
			return nil, fmt.Errorf("插件 %q 的 hook %q 与插件 key 冲突", key, hook)
		}
		if conflict := resolver.byHook[hook]; conflict != nil && conflict.key() != key {
			return nil, fmt.Errorf("插件 %q 的 hook %q 与插件 %q 冲突", key, hook, conflict.key())
		}
	}

	previous := resolver.byKey[key]
	if previous != nil && previous.hook() != "" && resolver.byHook[previous.hook()] == previous {
		delete(resolver.byHook, previous.hook())
	}
	resolver.byKey[key] = next
	if hook != "" {
		resolver.byHook[hook] = next
	}
	return previous, nil
}

// 按 key 优先、hook 回退解析并获取 generation 引用
func (resolver *resolver) resolve(target string) (*generation, error) {
	resolver.mu.RLock()
	defer resolver.mu.RUnlock()

	current := resolver.byKey[target]
	if current == nil {
		current = resolver.byHook[target]
	}
	if current == nil {
		return nil, protocol.NewError(protocol.ErrorNotFound, "插件不存在")
	}
	if !current.acquire() {
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件已停止接收新调用")
	}
	return current, nil
}

// 按版本移除 generation
func (resolver *resolver) remove(current *generation) *generation {
	if current == nil {
		return nil
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()

	if resolver.byKey[current.key()] != current {
		return nil
	}
	delete(resolver.byKey, current.key())
	if current.hook() != "" && resolver.byHook[current.hook()] == current {
		delete(resolver.byHook, current.hook())
	}
	return current
}

// 移除指定 key 的当前 generation
func (resolver *resolver) removeKey(key string) *generation {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()

	current := resolver.byKey[key]
	if current == nil {
		return nil
	}
	delete(resolver.byKey, key)
	if current.hook() != "" && resolver.byHook[current.hook()] == current {
		delete(resolver.byHook, current.hook())
	}

	return current
}
