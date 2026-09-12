package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/tetratelabs/wazero/api"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 宿主操作处理器
type HostHandler func(context.Context, int64, string, json.RawMessage) (json.RawMessage, error)

type responseHub struct {
	mu      sync.RWMutex
	buckets map[api.Module]*responseBucket
	handler HostHandler
	limit   uint32
}

type responseBucket struct {
	mu           sync.RWMutex
	next         uint64
	invocationID int64
	isActive     bool
	values       map[uint64][]byte
}

func newResponseHub(limit uint32, handler HostHandler) *responseHub {
	return &responseHub{
		buckets: make(map[api.Module]*responseBucket),
		handler: handler,
		limit:   limit,
	}
}

func (hub *responseHub) register(module api.Module) *responseBucket {
	bucket := &responseBucket{values: make(map[uint64][]byte)}
	hub.mu.Lock()
	hub.buckets[module] = bucket
	hub.mu.Unlock()

	return bucket
}

func (hub *responseHub) unregister(module api.Module) {
	hub.mu.Lock()
	delete(hub.buckets, module)
	hub.mu.Unlock()
}

func (hub *responseHub) bucket(module api.Module) (*responseBucket, bool) {
	hub.mu.RLock()
	bucket, exists := hub.buckets[module]
	hub.mu.RUnlock()

	return bucket, exists
}

func (bucket *responseBucket) begin(invocationID int64) error {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if bucket.isActive {
		return errors.New("插件实例已有执行中的调用")
	}
	bucket.invocationID = invocationID
	bucket.isActive = true

	return nil
}

func (bucket *responseBucket) end() {
	bucket.mu.Lock()
	bucket.invocationID = 0
	bucket.isActive = false
	clear(bucket.values)
	bucket.mu.Unlock()
}

func (bucket *responseBucket) add(value []byte) int64 {
	bucket.mu.Lock()
	bucket.next++
	if bucket.next == 0 {
		bucket.next++
	}
	handle := bucket.next
	bucket.values[handle] = append([]byte(nil), value...)
	bucket.mu.Unlock()

	return int64(handle)
}

func (bucket *responseBucket) length(handle int64) (uint32, bool) {
	bucket.mu.RLock()
	value, exists := bucket.values[uint64(handle)]
	bucket.mu.RUnlock()

	return uint32(len(value)), exists
}

func (bucket *responseBucket) write(handle int64, memory api.Memory, targetPointer, targetSize uint32) bool {
	bucket.mu.RLock()
	defer bucket.mu.RUnlock()
	value, exists := bucket.values[uint64(handle)]

	return exists && uint32(len(value)) == targetSize && memory.Write(targetPointer, value)
}

func (bucket *responseBucket) drop(handle int64) {
	bucket.mu.Lock()
	delete(bucket.values, uint64(handle))
	bucket.mu.Unlock()
}

func (hub *responseHub) call(ctx context.Context, module api.Module, invocationID int64, operationPointer, operationSize, inputPointer, inputSize uint32) int64 {
	bucket, exists := hub.bucket(module)
	if !exists || !bucket.matches(invocationID) {
		return 0
	}
	if operationSize == 0 || operationSize > hub.limit || inputSize > hub.limit {
		return bucket.add(hub.failure(protocol.ErrorResourceExhausted, "宿主调用载荷超限"))
	}
	memory := module.Memory()
	if memory == nil {
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "插件内存不可用"))
	}
	operation, ok := memory.Read(operationPointer, operationSize)
	if !ok {
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "宿主操作名内存无效"))
	}
	input, ok := memory.Read(inputPointer, inputSize)
	if !ok {
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "宿主调用参数内存无效"))
	}
	if hub.handler == nil {
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "宿主操作未注册"))
	}
	result, err := hub.handler(ctx, invocationID, string(append([]byte(nil), operation...)), append(json.RawMessage(nil), input...))
	if err != nil {
		var pluginError *protocol.PluginError
		if errors.As(err, &pluginError) && pluginError != nil {
			return bucket.add(hub.failure(pluginError.Code, pluginError.Message))
		}
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "宿主操作执行失败"))
	}
	payload, err := protocol.EncodeSuccess(result)
	if err != nil {
		return bucket.add(hub.failure(protocol.ErrorHostCallFailed, "宿主响应不是合法 JSON"))
	}
	if len(payload) > int(hub.limit) {
		return bucket.add(hub.failure(protocol.ErrorResourceExhausted, "宿主响应载荷超限"))
	}

	return bucket.add(payload)
}

func (hub *responseHub) length(_ context.Context, module api.Module, handle int64) int32 {
	bucket, exists := hub.bucket(module)
	if !exists {
		return -1
	}
	length, exists := bucket.length(handle)
	if !exists || length > hub.limit {
		return -1
	}

	return int32(length)
}

func (hub *responseHub) read(_ context.Context, module api.Module, handle int64, targetPointer, targetSize uint32) int32 {
	bucket, exists := hub.bucket(module)
	if !exists {
		return -1
	}
	memory := module.Memory()
	if memory == nil || !bucket.write(handle, memory, targetPointer, targetSize) {
		return -1
	}

	return int32(targetSize)
}

func (hub *responseHub) drop(_ context.Context, module api.Module, handle int64) {
	if bucket, exists := hub.bucket(module); exists {
		bucket.drop(handle)
	}
}

func (hub *responseHub) failure(code protocol.ErrorCode, message string) []byte {
	payload, err := protocol.EncodeFailure(code, message)
	if err != nil {
		panic(err)
	}

	return payload
}

func (bucket *responseBucket) matches(invocationID int64) bool {
	bucket.mu.Lock()
	matches := bucket.isActive && bucket.invocationID == invocationID
	bucket.mu.Unlock()

	return matches
}
