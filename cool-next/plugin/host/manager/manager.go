package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

const hostSource = "host"

// Host API 调用插件所需的最小接口
type Invoker interface {
	InvokeJSON(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}

// 固定 generation 后的调用输入准备器
type InputPreparer func(GenerationMetadata) (json.RawMessage, func(), error)

// 隔离 Manager 与 Host API 包依赖的 Host Handler 构造器
type HostFactory func(Invoker) (runtime.HostHandler, error)

// 插件 generation 调用与生命周期管理器
type Manager struct {
	mu           sync.Mutex
	config       Config
	store        Store
	resolver     *resolver
	runtime      *runtime.Runtime
	cache        *compileCache
	globalQuota  chan struct{}
	generations  map[*generation]bool
	order        []*generation
	diagnostics  map[string]Diagnostic
	syncFailures map[string]syncFailure
	drains       sync.WaitGroup
	syncCancel   context.CancelFunc
	syncDone     chan struct{}
	nextID       int64
	isStarted    bool
	isClosed     bool
	closeDone    chan struct{}
	drainErr     error
	closeErr     error
}

// 创建唯一 WASM Runtime 的插件 Manager
func New(ctx context.Context, config Config, store Store, hostFactory HostFactory) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("插件 Manager 配置无效: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager := &Manager{
		config:       config,
		store:        store,
		resolver:     newResolver(),
		globalQuota:  make(chan struct{}, config.MaxInstances),
		generations:  make(map[*generation]bool),
		diagnostics:  make(map[string]Diagnostic),
		syncFailures: make(map[string]syncFailure),
		closeDone:    make(chan struct{}),
	}

	var handler runtime.HostHandler
	if hostFactory != nil {
		var err error
		handler, err = hostFactory(manager)
		if err != nil {
			return nil, fmt.Errorf("创建插件 Host Handler 失败: %w", err)
		}
	}
	runtime, err := runtime.New(ctx, config.Runtime, handler)
	if err != nil {
		return nil, err
	}
	manager.runtime = runtime
	manager.cache = newCompileCache(runtime)

	return manager, nil
}

func (manager *Manager) disable(key string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed {
		return errors.New("插件 Manager 已关闭")
	}
	if current := manager.resolver.removeKey(key); current != nil {
		manager.scheduleDrainLocked(current)
	}

	return nil
}

// 在数据库事务提交后禁用本节点插件 generation
func (manager *Manager) Disable(desired DesiredPlugin) error {
	if manager == nil || desired.Key == "" {
		return errors.New("插件禁用目标无效")
	}
	if err := manager.disable(desired.Key); err != nil {
		manager.setDiagnostic(desired, "error", err)

		return err
	}
	manager.setDiagnostic(desired, "disabled", nil)

	return nil
}

// 在数据库事务提交后卸载本节点插件 generation
func (manager *Manager) Remove(key string) error {
	if manager == nil || key == "" {
		return errors.New("插件卸载目标无效")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed {
		return errors.New("插件 Manager 已关闭")
	}
	if current := manager.resolver.removeKey(key); current != nil {
		manager.scheduleDrainLocked(current)
	}
	delete(manager.diagnostics, key)

	return nil
}

// 按 key 或 hook 调用插件方法
func (manager *Manager) InvokeJSON(
	ctx context.Context,
	target string,
	method string,
	input json.RawMessage,
) (json.RawMessage, error) {
	return manager.invokeJSON(ctx, target, method, input, nil)
}

// 固定目标 generation 后准备输入并调用插件
func (manager *Manager) InvokeJSONPrepared(
	ctx context.Context,
	target string,
	method string,
	prepare InputPreparer,
) (json.RawMessage, error) {
	if manager == nil {
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件 Manager 不可用")
	}
	if prepare == nil {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "插件调用输入准备器不能为空")
	}
	return manager.invokeJSON(ctx, target, method, nil, prepare)
}

func (manager *Manager) invokeJSON(
	ctx context.Context,
	target string,
	method string,
	input json.RawMessage,
	prepare InputPreparer,
) (json.RawMessage, error) {
	if manager == nil {
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件 Manager 不可用")
	}
	callCtx, cancelCall := withCallTimeout(ctx, manager.config.CallTimeout)
	defer cancelCall()

	invocation, hasInvocation := InvocationFromContext(callCtx)
	manager.mu.Lock()
	if manager.isClosed {
		manager.mu.Unlock()
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件 Manager 已关闭")
	}
	if !hasInvocation || invocation.ID == 0 {
		invocation.ID = manager.nextIDLocked()
	}
	if invocation.Source == "" {
		invocation.Source = hostSource
	}
	callCtx = withInvocation(callCtx, invocation)
	current, err := manager.resolver.resolve(target)
	manager.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer current.release()

	callCtx, err = enterPlugin(callCtx, current.key(), manager.config.MaxCallDepth)
	if err != nil {
		return nil, err
	}
	metadata := GenerationMetadata{
		Key:     current.key(),
		Version: current.metadata.Version,
		Config:  current.configuration,
	}
	callCtx = withGenerationSnapshot(callCtx, metadata)
	if prepare != nil {
		metadata.Config = append(json.RawMessage(nil), metadata.Config...)
		var cleanup func()
		input, cleanup, err = prepare(metadata)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, err
		}
	}
	result, invokeErr := current.invoke(callCtx, invocation.ID, method, input)
	if current.metadata.Singleton && pluginErrorCode(invokeErr) == protocol.ErrorTimeout && current.isDraining() {
		manager.retireSingleton(current)
	}
	return result, invokeErr
}

// 创建共享配额约束下的 generation 候选
func (manager *Manager) createGeneration(
	desired DesiredPlugin,
	reference *compiledRef,
	singleton *runtime.Instance,
) (*generation, error) {
	if manager == nil {
		return nil, errors.New("插件 Manager 不可用")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed {
		return nil, errors.New("插件 Manager 已关闭")
	}
	limit := manager.config.MaxInstancesPerPlugin
	if desired.Singleton {
		limit = 1
	}
	return newGeneration(
		desired,
		reference,
		singleton,
		manager.globalQuota,
		make(chan struct{}, limit),
		manager.config.ShutdownTimeout,
	), nil
}

// 原子发布 generation，新调用立即解析到新版本
func (manager *Manager) publish(next *generation) error {
	if manager == nil {
		return errors.New("插件 Manager 不可用")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed {
		return errors.New("插件 Manager 已关闭")
	}
	if _, exists := manager.generations[next]; exists {
		return errors.New("插件 generation 已由 Manager 跟踪")
	}
	previous, err := manager.resolver.publish(next)
	if err != nil {
		return err
	}
	manager.generations[next] = false
	manager.order = append(manager.order, next)
	if previous != nil && previous != next {
		manager.scheduleDrainLocked(previous)
	}
	return nil
}

// 按 generation 指针移除已损坏单例
func (manager *Manager) retireSingleton(current *generation) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed || manager.resolver.remove(current) == nil {
		return
	}
	manager.scheduleDrainLocked(current)
}

// 拒绝新调用并等待所有 generation 排空后关闭 Runtime
func (manager *Manager) Close(ctx context.Context) error {
	if manager == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	syncErr := manager.stopSync(ctx)

	manager.mu.Lock()
	if !manager.isClosed {
		manager.isClosed = true
		var previousDone <-chan struct{}
		for index := len(manager.order) - 1; index >= 0; index-- {
			current := manager.order[index]
			manager.resolver.remove(current)
			previousDone = manager.scheduleAfterLocked(current, previousDone)
		}
		go manager.finishClose()
	}
	done := manager.closeDone
	manager.mu.Unlock()

	select {
	case <-done:
		manager.mu.Lock()
		err := manager.closeErr
		manager.mu.Unlock()
		return errors.Join(syncErr, err)
	case <-ctx.Done():
		return errors.Join(syncErr, ctx.Err())
	}
}

func (manager *Manager) scheduleDrainLocked(current *generation) {
	manager.scheduleAfterLocked(current, nil)
}

func (manager *Manager) scheduleAfterLocked(current *generation, previousDone <-chan struct{}) <-chan struct{} {
	if current == nil || manager.generations[current] {
		if current == nil {
			return previousDone
		}

		return current.shutdownDone
	}
	manager.generations[current] = true
	current.beginDrain()
	invocationID := manager.nextIDLocked()
	manager.drains.Add(1)
	go func() {
		defer manager.drains.Done()
		if previousDone != nil {
			<-previousDone
		}
		err := current.shutdown(context.Background(), invocationID)
		manager.mu.Lock()
		delete(manager.generations, current)
		manager.removeOrderLocked(current)
		manager.drainErr = errors.Join(manager.drainErr, err)
		manager.mu.Unlock()
	}()

	return current.shutdownDone
}

func (manager *Manager) removeOrderLocked(current *generation) {
	for index, tracked := range manager.order {
		if tracked != current {
			continue
		}
		manager.order = append(manager.order[:index], manager.order[index+1:]...)

		return
	}
}

func (manager *Manager) finishClose() {
	manager.drains.Wait()
	cacheErr := manager.cache.close(context.Background())
	runtimeErr := manager.runtime.Close(context.Background())

	manager.mu.Lock()
	manager.closeErr = errors.Join(manager.drainErr, cacheErr, runtimeErr)
	close(manager.closeDone)
	manager.mu.Unlock()
}

func (manager *Manager) nextIDLocked() int64 {
	if manager.nextID == math.MaxInt64 {
		manager.nextID = 0
	}
	manager.nextID++
	return manager.nextID
}

func (manager *Manager) nextInvocationID() int64 {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	return manager.nextIDLocked()
}
