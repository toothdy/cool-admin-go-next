package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
)

// WASM 编译缓存
type compileCache struct {
	mu        sync.Mutex
	compile   func(context.Context, string, []byte) (*runtime.Compiled, error)
	entries   map[string]*compileEntry
	isClosed  bool
	closeDone chan struct{}
	closeErr  error
}

// 单个 SHA 对应的编译缓存项
type compileEntry struct {
	mu          sync.RWMutex
	sha         string
	compiled    *runtime.Compiled
	err         error
	references  int
	isCompiling bool
	isClosed    bool
	done        chan struct{}
	cancel      context.CancelFunc
}

// 编译缓存引用
type compiledRef struct {
	cache      *compileCache
	entry      *compileEntry
	isReleased bool
}

// 创建 WASM 编译缓存
func newCompileCache(r *runtime.Runtime) *compileCache {
	return &compileCache{
		compile: func(ctx context.Context, _ string, wasm []byte) (*runtime.Compiled, error) {
			return r.Compile(ctx, wasm)
		},
		entries:   make(map[string]*compileEntry),
		closeDone: make(chan struct{}),
	}
}

// 返回引用持有的编译产物
func (reference *compiledRef) compiled() *runtime.Compiled {
	if reference == nil || reference.entry == nil {
		return nil
	}
	reference.entry.mu.RLock()
	defer reference.entry.mu.RUnlock()
	if reference.isReleased || reference.entry.isClosed {
		return nil
	}

	return reference.entry.compiled
}

// 获取指定制品 SHA 的编译引用
func (cache *compileCache) acquire(ctx context.Context, sha string, wasm []byte) (*compiledRef, error) {
	if err := checkSHA(sha); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cache.mu.Lock()
	if cache.isClosed {
		cache.mu.Unlock()
		return nil, errors.New("插件编译缓存已关闭")
	}
	entry := cache.entries[sha]
	isCompiler := entry == nil
	if isCompiler {
		compileCtx, cancelCompile := context.WithCancel(context.Background())
		wasmSnapshot := bytes.Clone(wasm)
		entry = &compileEntry{
			sha:         sha,
			references:  1,
			isCompiling: true,
			done:        make(chan struct{}),
			cancel:      cancelCompile,
		}
		cache.entries[sha] = entry
		go func() {
			compiled, err := cache.compile(compileCtx, sha, wasmSnapshot)
			cache.completeCompile(entry, compiled, err)
		}()
	} else {
		entry.references++
	}
	cache.mu.Unlock()

	reference := &compiledRef{cache: cache, entry: entry}
	select {
	case <-entry.done:
		entry.mu.RLock()
		compiled, compileErr, isClosed := entry.compiled, entry.err, entry.isClosed
		entry.mu.RUnlock()
		if compileErr != nil {
			_ = cache.release(context.Background(), reference)
			return nil, compileErr
		}
		if isClosed || compiled == nil {
			_ = cache.release(context.Background(), reference)
			return nil, errors.New("插件编译缓存已关闭")
		}
		return reference, nil
	case <-ctx.Done():
		_ = cache.release(context.Background(), reference)
		return nil, ctx.Err()
	}
}

func (cache *compileCache) completeCompile(entry *compileEntry, compiled *runtime.Compiled, compileErr error) {
	cache.mu.Lock()
	entry.mu.Lock()
	entry.compiled = compiled
	entry.err = compileErr
	entry.isCompiling = false
	entry.cancel()
	shouldClose := compileErr != nil || cache.isClosed || entry.references == 0
	if shouldClose {
		if cache.entries[entry.sha] == entry {
			delete(cache.entries, entry.sha)
		}
		entry.isClosed = true
	}
	close(entry.done)
	var closeCompiled *runtime.Compiled
	if shouldClose && !cache.isClosed {
		closeCompiled = entry.compiled
		entry.compiled = nil
	}
	entry.mu.Unlock()
	cache.mu.Unlock()

	if closeCompiled != nil {
		_ = closeCompiled.Close(context.Background())
	}
}

// 释放一个编译缓存引用
func (cache *compileCache) release(ctx context.Context, reference *compiledRef) error {
	if reference == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cache.mu.Lock()
	if reference.cache != cache {
		cache.mu.Unlock()
		return errors.New("编译引用不属于当前缓存")
	}
	entry := reference.entry
	if entry == nil {
		cache.mu.Unlock()
		return nil
	}
	entry.mu.Lock()
	if reference.isReleased {
		entry.mu.Unlock()
		cache.mu.Unlock()
		return nil
	}
	reference.isReleased = true
	if entry.references <= 0 {
		entry.mu.Unlock()
		cache.mu.Unlock()
		return errors.New("编译缓存引用计数无效")
	}
	entry.references--
	var closeCompiled *runtime.Compiled
	if entry.references == 0 {
		if entry.isCompiling {
			entry.cancel()
		} else {
			if cache.entries[entry.sha] == entry {
				delete(cache.entries, entry.sha)
			}
			if !entry.isClosed {
				entry.isClosed = true
				closeCompiled = entry.compiled
				entry.compiled = nil
			}
		}
	}
	entry.mu.Unlock()
	cache.mu.Unlock()

	if closeCompiled != nil {
		return closeCompiled.Close(ctx)
	}
	return nil
}

// 关闭全部编译缓存并拒绝新引用
func (cache *compileCache) close(ctx context.Context) error {
	if cache == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cache.mu.Lock()
	if !cache.isClosed {
		cache.isClosed = true
		entries := make([]*compileEntry, 0, len(cache.entries))
		for sha, entry := range cache.entries {
			delete(cache.entries, sha)
			entry.mu.Lock()
			entry.isClosed = true
			entry.cancel()
			entry.mu.Unlock()
			entries = append(entries, entry)
		}
		go cache.finishClose(entries)
	}
	done := cache.closeDone
	cache.mu.Unlock()

	select {
	case <-done:
		cache.mu.Lock()
		closeErr := cache.closeErr
		cache.mu.Unlock()
		return closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (cache *compileCache) finishClose(entries []*compileEntry) {
	var closeErr error
	for _, entry := range entries {
		<-entry.done
		entry.mu.Lock()
		compiled := entry.compiled
		entry.compiled = nil
		entry.mu.Unlock()
		if compiled != nil {
			closeErr = errors.Join(closeErr, compiled.Close(context.Background()))
		}
	}

	cache.mu.Lock()
	cache.closeErr = closeErr
	close(cache.closeDone)
	cache.mu.Unlock()
}

// 校验制品 SHA-256 文本
func checkSHA(sha string) error {
	if len(sha) != 64 {
		return fmt.Errorf("插件制品 SHA-256 长度无效: %d", len(sha))
	}
	for _, character := range sha {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("插件制品 SHA-256 必须是小写十六进制")
		}
	}

	return nil
}
