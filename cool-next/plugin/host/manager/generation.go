package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 一次原子发布使用的不可变插件快照
type generation struct {
	metadata        DesiredPlugin
	configuration   json.RawMessage
	reference       *compiledRef
	singleton       *runtime.Instance
	globalQuota     chan struct{}
	pluginQuota     chan struct{}
	shutdownTimeout time.Duration
	lifecycle       context.Context
	cancelLifecycle context.CancelFunc

	mu       sync.Mutex
	active   int
	draining bool
	drained  chan struct{}

	shutdownMu     sync.Mutex
	isShuttingDown bool
	shutdownDone   chan struct{}
	shutdownErr    error
}

// 创建不可变插件快照
func newGeneration(
	desired DesiredPlugin,
	reference *compiledRef,
	singleton *runtime.Instance,
	globalQuota chan struct{},
	pluginQuota chan struct{},
	shutdownTimeout time.Duration,
) *generation {
	metadata := cloneDesiredPlugin(desired)
	config := metadata.Config
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	configuration, err := json.Marshal(config)
	if err != nil {
		panic(fmt.Sprintf("插件配置快照无效: %v", err))
	}
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())

	return &generation{
		metadata:        metadata,
		configuration:   configuration,
		reference:       reference,
		singleton:       singleton,
		globalQuota:     globalQuota,
		pluginQuota:     pluginQuota,
		shutdownTimeout: shutdownTimeout,
		lifecycle:       lifecycle,
		cancelLifecycle: cancelLifecycle,
		drained:         make(chan struct{}),
		shutdownDone:    make(chan struct{}),
	}
}

// 返回 generation 的插件 key
func (generation *generation) key() string {
	if generation == nil {
		return ""
	}
	return generation.metadata.Key
}

// 返回 generation 的插件 hook
func (generation *generation) hook() string {
	if generation == nil {
		return ""
	}
	return generation.metadata.Hook
}

// 获取一次活跃调用引用
func (generation *generation) acquire() bool {
	if generation == nil {
		return false
	}
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.draining {
		return false
	}
	generation.active++

	return true
}

// 释放一次活跃调用引用
func (generation *generation) release() {
	if generation == nil {
		return
	}
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.active <= 0 {
		panic("插件 generation 活跃引用计数无效")
	}
	generation.active--
	if generation.draining && generation.active == 0 {
		close(generation.drained)
	}
}

// 返回 generation 是否停止接收新调用
func (generation *generation) isDraining() bool {
	if generation == nil {
		return true
	}
	generation.mu.Lock()
	defer generation.mu.Unlock()

	return generation.draining
}

// 停止接收新调用，并在活跃引用归零时发出信号
func (generation *generation) beginDrain() {
	if generation == nil {
		return
	}
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.draining {
		return
	}
	generation.draining = true
	if generation.active == 0 {
		close(generation.drained)
	}
}

// 等待全部活跃调用释放
func (generation *generation) waitDrained(ctx context.Context) error {
	if generation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-generation.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// 使用当前 generation 快照调用插件
func (generation *generation) invoke(
	ctx context.Context,
	invocationID int64,
	method string,
	input json.RawMessage,
) (json.RawMessage, error) {
	if generation == nil {
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件 generation 不可用")
	}
	callCtx, cancelCall := generation.callContext(ctx)
	defer cancelCall()
	releaseQuota, err := acquireQuotas(callCtx, generation.globalQuota, generation.pluginQuota)
	if err != nil {
		return nil, err
	}
	defer releaseQuota()

	if generation.metadata.Singleton {
		if generation.singleton == nil {
			return nil, protocol.NewError(protocol.ErrorDisabled, "插件单例尚未初始化")
		}
		result, invokeErr := generation.singleton.Invoke(callCtx, invocationID, method, input)
		if pluginErrorCode(invokeErr) == protocol.ErrorTimeout &&
			(errors.Is(invokeErr, context.Canceled) || errors.Is(invokeErr, context.DeadlineExceeded)) {
			// wazero 会在调用超时后关闭 Module，损坏的单例不能继续接收调用
			generation.beginDrain()
		}
		return result, invokeErr
	}

	compiled := generation.reference.compiled()
	if compiled == nil {
		return nil, protocol.NewError(protocol.ErrorDisabled, "插件编译产物不可用")
	}
	instance, err := compiled.InstantiateForPlugin(callCtx, generation.metadata.Key)
	if err != nil {
		return nil, err
	}
	if err = instance.Initialize(callCtx, invocationID, generation.configuration); err != nil {
		return nil, errors.Join(err, instance.Close(context.Background()))
	}
	result, invokeErr := instance.Invoke(callCtx, invocationID, method, input)
	shutdownErr := instance.Shutdown(callCtx, invocationID)
	closeErr := instance.Close(context.Background())

	return result, errors.Join(invokeErr, shutdownErr, closeErr)
}

// 排空并关闭 generation
func (generation *generation) shutdown(ctx context.Context, invocationID int64) error {
	if generation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	generation.shutdownMu.Lock()
	if generation.isShuttingDown {
		done := generation.shutdownDone
		generation.shutdownMu.Unlock()
		select {
		case <-done:
			return generation.shutdownErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	generation.isShuttingDown = true
	generation.shutdownMu.Unlock()

	shutdownErr := generation.shutdownOnce(ctx, invocationID)
	generation.shutdownMu.Lock()
	generation.shutdownErr = shutdownErr
	close(generation.shutdownDone)
	generation.shutdownMu.Unlock()

	return shutdownErr
}

func (generation *generation) shutdownOnce(ctx context.Context, invocationID int64) error {
	generation.beginDrain()
	shutdownCtx, cancelShutdown := context.WithTimeout(ctx, generation.shutdownTimeout)
	defer cancelShutdown()
	if err := generation.waitDrained(shutdownCtx); err != nil {
		generation.cancelLifecycle()
		return errors.Join(
			protocol.WrapError(protocol.ErrorTimeout, "等待插件调用排空超时", err),
			generation.closeSingleton(),
			generation.releaseCompiled(),
		)
	}

	var shutdownErr error
	if generation.singleton != nil {
		shutdownErr = generation.singleton.Shutdown(shutdownCtx, invocationID)
	}
	generation.cancelLifecycle()

	return errors.Join(shutdownErr, generation.closeSingleton(), generation.releaseCompiled())
}

func (generation *generation) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancelCall := context.WithCancel(ctx)
	stopOnShutdown := context.AfterFunc(generation.lifecycle, cancelCall)

	return callCtx, func() {
		stopOnShutdown()
		cancelCall()
	}
}

func (generation *generation) closeSingleton() error {
	if generation.singleton == nil {
		return nil
	}
	return generation.singleton.Close(context.Background())
}

func (generation *generation) releaseCompiled() error {
	if generation.reference == nil {
		return nil
	}
	if generation.reference.cache == nil {
		return errors.New("插件编译引用没有所属缓存")
	}

	return generation.reference.cache.release(context.Background(), generation.reference)
}

// 获取全局和单插件实例配额
func acquireQuotas(
	ctx context.Context,
	globalQuota chan struct{},
	pluginQuota chan struct{},
) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := acquireQuota(ctx, pluginQuota); err != nil {
		return nil, err
	}
	if err := acquireQuota(ctx, globalQuota); err != nil {
		releaseQuota(pluginQuota)
		return nil, err
	}

	return func() {
		releaseQuota(globalQuota)
		releaseQuota(pluginQuota)
	}, nil
}

func acquireQuota(ctx context.Context, quota chan struct{}) error {
	if quota == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return protocol.WrapError(protocol.ErrorTimeout, "等待插件实例配额已取消或超时", err)
	}
	select {
	case quota <- struct{}{}:
		if err := ctx.Err(); err != nil {
			releaseQuota(quota)
			return protocol.WrapError(protocol.ErrorTimeout, "等待插件实例配额已取消或超时", err)
		}
		return nil
	case <-ctx.Done():
		return protocol.WrapError(protocol.ErrorTimeout, "等待插件实例配额已取消或超时", ctx.Err())
	}
}

func releaseQuota(quota chan struct{}) {
	if quota != nil {
		<-quota
	}
}

func cloneDesiredPlugin(desired DesiredPlugin) DesiredPlugin {
	desired.Config = cloneRawMessages(desired.Config)
	desired.Manifest.Config = cloneRawMessages(desired.Manifest.Config)

	return desired
}

func cloneRawMessages(values map[string]json.RawMessage) map[string]json.RawMessage {
	if values == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		result[key] = append(json.RawMessage(nil), value...)
	}

	return result
}

func pluginErrorCode(err error) protocol.ErrorCode {
	var pluginError *protocol.PluginError
	if errors.As(err, &pluginError) {
		return pluginError.Code
	}

	return ""
}
