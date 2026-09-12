package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

var defaultRuntime = newGuestRuntime(callHostABI)

type guestRuntime struct {
	mu          sync.Mutex
	definition  Definition
	isDefined   bool
	isReady     bool
	isShutdown  bool
	config      json.RawMessage
	call        hostCaller
	allocations *allocationStore
	responses   *responseStore
}

func newGuestRuntime(call hostCaller) *guestRuntime {
	return &guestRuntime{
		call:        call,
		allocations: newAllocationStore(),
		responses:   newResponseStore(),
	}
}

// 注册当前 WASM 实例的插件定义
func Register(definition Definition) {
	if err := defaultRuntime.register(definition); err != nil {
		panic(err)
	}
}

func (runtime *guestRuntime) register(definition Definition) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	if runtime.isDefined {
		return errors.New("插件定义只能注册一次")
	}
	if !definition.valid {
		return errors.New("插件定义无效")
	}
	runtime.definition = definition.clone()
	runtime.isDefined = true

	return nil
}

func (runtime *guestRuntime) initialize(invocationID int64, config json.RawMessage) int64 {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	if !runtime.isDefined {
		return runtime.failure(protocol.ErrorInitFailed, "插件尚未注册")
	}
	if runtime.isReady || runtime.isShutdown {
		return runtime.failure(protocol.ErrorInitFailed, "插件实例已初始化")
	}
	if !isJSONObject(config) {
		return runtime.failure(protocol.ErrorInvalidInput, "插件配置必须是 JSON object")
	}
	ctx := withInvocation(context.Background(), invocationID, config, runtime.call)
	if runtime.definition.ready != nil {
		if err := callLifecycle(ctx, runtime.definition.ready); err != nil {
			return runtime.failureFrom(err, protocol.ErrorInitFailed, "插件初始化失败")
		}
	}
	runtime.config = append(json.RawMessage(nil), config...)
	runtime.isReady = true

	return runtime.success(nil)
}

func (runtime *guestRuntime) invoke(invocationID int64, method string, input json.RawMessage) int64 {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	if !runtime.isReady || runtime.isShutdown {
		return runtime.failure(protocol.ErrorDisabled, "插件实例不可调用")
	}
	handler, exists := runtime.definition.methods[method]
	if !exists {
		return runtime.failure(protocol.ErrorMethodNotFound, "插件方法不存在")
	}
	if len(input) == 0 || !json.Valid(input) {
		return runtime.failure(protocol.ErrorInvalidInput, "插件方法参数不是合法 JSON")
	}
	ctx := withInvocation(context.Background(), invocationID, runtime.config, runtime.call)
	result, err := callHandler(ctx, handler, input)
	if err != nil {
		return runtime.failureFrom(err, protocol.ErrorTrap, "插件方法执行失败")
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if !json.Valid(result) {
		return runtime.failure(protocol.ErrorInvalidOutput, "插件方法响应不是合法 JSON")
	}

	return runtime.success(result)
}

func (runtime *guestRuntime) shutdown(invocationID int64) int64 {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	if runtime.isShutdown {
		return runtime.success(nil)
	}
	if !runtime.isReady {
		return runtime.failure(protocol.ErrorDisabled, "插件实例尚未初始化")
	}
	ctx := withInvocation(context.Background(), invocationID, runtime.config, runtime.call)
	if runtime.definition.shutdown != nil {
		if err := callLifecycle(ctx, runtime.definition.shutdown); err != nil {
			return runtime.failureFrom(err, protocol.ErrorTrap, "插件关闭失败")
		}
	}
	runtime.isShutdown = true
	runtime.isReady = false

	return runtime.success(nil)
}

func (runtime *guestRuntime) initializeMemory(invocationID int64, pointer unsafe.Pointer, size int32) int64 {
	config, err := runtime.allocations.read(pointer, size)
	if err != nil {
		return runtime.failureFrom(err, protocol.ErrorInvalidInput, "插件配置内存无效")
	}

	return runtime.initialize(invocationID, config)
}

func (runtime *guestRuntime) invokeMemory(
	invocationID int64,
	methodPointer unsafe.Pointer,
	methodSize int32,
	inputPointer unsafe.Pointer,
	inputSize int32,
) int64 {
	method, err := runtime.allocations.read(methodPointer, methodSize)
	if err != nil {
		return runtime.failureFrom(err, protocol.ErrorInvalidInput, "插件方法名内存无效")
	}
	input, err := runtime.allocations.read(inputPointer, inputSize)
	if err != nil {
		return runtime.failureFrom(err, protocol.ErrorInvalidInput, "插件参数内存无效")
	}

	return runtime.invoke(invocationID, string(method), input)
}

func (runtime *guestRuntime) success(data json.RawMessage) int64 {
	payload, err := protocol.EncodeSuccess(data)
	if err != nil {
		return runtime.failureFrom(err, protocol.ErrorInvalidOutput, "插件响应编码失败")
	}

	return runtime.responses.add(payload)
}

func (runtime *guestRuntime) failure(code protocol.ErrorCode, message string) int64 {
	payload, err := protocol.EncodeFailure(code, message)
	if err != nil {
		panic(err)
	}

	return runtime.responses.add(payload)
}

func (runtime *guestRuntime) failureFrom(err error, fallbackCode protocol.ErrorCode, fallbackMessage string) int64 {
	var pluginError *protocol.PluginError
	if errors.As(err, &pluginError) && pluginError != nil && pluginError.Code != "" && pluginError.Message != "" {
		return runtime.failure(pluginError.Code, pluginError.Message)
	}

	return runtime.failure(fallbackCode, fallbackMessage)
}

func callHandler(ctx context.Context, handler RawHandler, input json.RawMessage) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("插件方法 Panic: %v", recovered)
		}
	}()

	return handler(ctx, append(json.RawMessage(nil), input...))
}

func callLifecycle(ctx context.Context, handler LifecycleHandler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("插件生命周期 Panic: %v", recovered)
		}
	}()

	return handler(ctx)
}

func isJSONObject(value json.RawMessage) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}
