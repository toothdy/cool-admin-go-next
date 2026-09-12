package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 已实例化的 WASM 插件
type Instance struct {
	callMu     sync.Mutex
	closeMu    sync.Mutex
	runtime    *Runtime
	module     api.Module
	bucket     *responseBucket
	functions  map[string]api.Function
	maxPayload uint32
	isClosed   bool
}

func newInstance(runtime *Runtime, module api.Module, bucket *responseBucket) *Instance {
	functions := make(map[string]api.Function)
	for _, signature := range protocol.GuestExports() {
		functions[signature.Name] = module.ExportedFunction(signature.Name)
	}

	return &Instance{
		runtime:    runtime,
		module:     module,
		bucket:     bucket,
		functions:  functions,
		maxPayload: runtime.config.MaxPayloadBytes,
	}
}

// 使用 JSON object 配置初始化实例
func (instance *Instance) Initialize(ctx context.Context, invocationID int64, config json.RawMessage) error {
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	if !isJSONObject(config) {
		return protocol.NewError(protocol.ErrorInvalidInput, "插件配置必须是 JSON object")
	}
	_, err := instance.call(ctx, invocationID, protocol.ExportInit, config)

	return err
}

// 调用插件任意方法
func (instance *Instance) Invoke(ctx context.Context, invocationID int64, method string, input json.RawMessage) (json.RawMessage, error) {
	if method == "" {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "插件方法名不能为空")
	}
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	if !json.Valid(input) {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "插件方法参数不是合法 JSON")
	}
	return instance.call(ctx, invocationID, protocol.ExportInvoke, []byte(method), input)
}

// 执行 Guest Shutdown
func (instance *Instance) Shutdown(ctx context.Context, invocationID int64) error {
	_, err := instance.call(ctx, invocationID, protocol.ExportShutdown)

	return err
}

// 关闭插件实例
func (instance *Instance) Close(ctx context.Context) error {
	instance.closeMu.Lock()
	defer instance.closeMu.Unlock()
	if instance.isClosed {
		return nil
	}
	instance.isClosed = true
	instance.runtime.host.unregister(instance.module)

	return instance.module.Close(ctx)
}

func (instance *Instance) abiVersion(ctx context.Context) (uint32, error) {
	results, err := instance.functions[protocol.ExportABIVersion].Call(ctx)
	if err != nil {
		return 0, mapRuntimeError(err, protocol.ErrorABIUnsupported, "读取插件 ABI 版本失败")
	}
	if len(results) != 1 {
		return 0, protocol.NewError(protocol.ErrorABIUnsupported, "插件 ABI 版本响应无效")
	}

	return api.DecodeU32(results[0]), nil
}

func (instance *Instance) call(ctx context.Context, invocationID int64, functionName string, inputs ...[]byte) (json.RawMessage, error) {
	instance.callMu.Lock()
	defer instance.callMu.Unlock()
	instance.closeMu.Lock()
	isClosed := instance.isClosed
	instance.closeMu.Unlock()
	if isClosed || instance.module.IsClosed() {
		return nil, errors.New("WASM 插件实例已关闭")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, input := range inputs {
		if len(input) > int(instance.maxPayload) {
			return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件输入载荷超限")
		}
	}
	if err := instance.bucket.begin(invocationID); err != nil {
		return nil, err
	}
	defer instance.bucket.end()

	parameters := []uint64{api.EncodeI64(invocationID)}
	allocations := make([]guestAllocation, 0, len(inputs))
	for _, input := range inputs {
		allocation, err := instance.writeGuest(ctx, input)
		if err != nil {
			instance.freeGuest(ctx, allocations)
			return nil, err
		}
		allocations = append(allocations, allocation)
		parameters = append(parameters, api.EncodeU32(allocation.pointer), api.EncodeU32(allocation.size))
	}
	results, err := instance.functions[functionName].Call(ctx, parameters...)
	instance.freeGuest(ctx, allocations)
	if err != nil {
		return nil, mapRuntimeError(err, protocol.ErrorTrap, "执行 WASM 插件失败")
	}
	if len(results) != 1 {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应句柄无效")
	}
	handle := int64(results[0])

	return instance.readGuestResponse(ctx, handle)
}

type guestAllocation struct {
	pointer uint32
	size    uint32
}

func (instance *Instance) writeGuest(ctx context.Context, value []byte) (guestAllocation, error) {
	results, err := instance.functions[protocol.ExportAlloc].Call(ctx, api.EncodeU32(uint32(len(value))))
	if err != nil {
		return guestAllocation{}, mapRuntimeError(err, protocol.ErrorTrap, "分配插件内存失败")
	}
	if len(results) != 1 {
		return guestAllocation{}, protocol.NewError(protocol.ErrorABIUnsupported, "插件内存分配响应无效")
	}
	pointer := api.DecodeU32(results[0])
	if len(value) > 0 && (pointer == 0 || !instance.module.Memory().Write(pointer, value)) {
		return guestAllocation{}, protocol.NewError(protocol.ErrorTrap, "写入插件内存失败")
	}

	return guestAllocation{pointer: pointer, size: uint32(len(value))}, nil
}

func (instance *Instance) freeGuest(ctx context.Context, allocations []guestAllocation) {
	if instance.module.IsClosed() {
		return
	}
	for _, allocation := range allocations {
		_, _ = instance.functions[protocol.ExportFree].Call(ctx, api.EncodeU32(allocation.pointer), api.EncodeU32(allocation.size))
	}
}

func (instance *Instance) readGuestResponse(ctx context.Context, handle int64) (json.RawMessage, error) {
	if handle <= 0 {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应句柄无效")
	}
	drop := instance.functions[protocol.ExportResponseDrop]
	defer func() {
		if !instance.module.IsClosed() {
			_, _ = drop.Call(ctx, uint64(handle))
		}
	}()
	lengthResult, err := instance.functions[protocol.ExportResponseLength].Call(ctx, uint64(handle))
	if err != nil {
		return nil, mapRuntimeError(err, protocol.ErrorTrap, "读取插件响应长度失败")
	}
	if len(lengthResult) != 1 {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应长度无效")
	}
	size := api.DecodeI32(lengthResult[0])
	if size < 0 {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应句柄不存在")
	}
	if uint32(size) > instance.maxPayload {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "插件响应载荷超限")
	}
	pointerResult, err := instance.functions[protocol.ExportResponsePointer].Call(ctx, uint64(handle))
	if err != nil {
		return nil, mapRuntimeError(err, protocol.ErrorTrap, "读取插件响应地址失败")
	}
	if len(pointerResult) != 1 {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应地址无效")
	}
	pointer := api.DecodeU32(pointerResult[0])
	value, ok := instance.module.Memory().Read(pointer, uint32(size))
	if !ok {
		return nil, protocol.NewError(protocol.ErrorInvalidOutput, "插件响应内存无效")
	}

	return protocol.Decode(append(json.RawMessage(nil), value...))
}

func mapRuntimeError(err error, fallbackCode protocol.ErrorCode, message string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return protocol.WrapError(protocol.ErrorTimeout, "插件调用已取消或超时", err)
	}
	var exitError *sys.ExitError
	if errors.As(err, &exitError) {
		if exitError.ExitCode() == sys.ExitCodeDeadlineExceeded || exitError.ExitCode() == sys.ExitCodeContextCanceled {
			return protocol.WrapError(protocol.ErrorTimeout, "插件调用已取消或超时", err)
		}
	}

	return protocol.WrapError(fallbackCode, message, err)
}

func isJSONObject(value json.RawMessage) bool {
	if !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func (instance *Instance) String() string {
	return fmt.Sprintf("plugin.Instance(%s)", instance.module.Name())
}
