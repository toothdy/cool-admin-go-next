package sdk

import (
	"unsafe"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 返回插件 ABI 版本
func ABIVersion() uint32 {
	return protocol.Version
}

// 分配宿主输入内存
func ABIAlloc(size int32) unsafe.Pointer {
	return defaultRuntime.allocations.allocate(size)
}

// 释放宿主输入内存
func ABIFree(pointer unsafe.Pointer, size int32) {
	defaultRuntime.allocations.free(pointer, size)
}

// 初始化插件实例
func ABIInit(invocationID int64, configPointer unsafe.Pointer, configSize int32) int64 {
	return defaultRuntime.initializeMemory(invocationID, configPointer, configSize)
}

// 调用插件方法
func ABIInvoke(
	invocationID int64,
	methodPointer unsafe.Pointer,
	methodSize int32,
	inputPointer unsafe.Pointer,
	inputSize int32,
) int64 {
	return defaultRuntime.invokeMemory(invocationID, methodPointer, methodSize, inputPointer, inputSize)
}

// 关闭插件实例
func ABIShutdown(invocationID int64) int64 {
	return defaultRuntime.shutdown(invocationID)
}

// 返回响应内存指针
func ABIResponsePointer(handle int64) unsafe.Pointer {
	return defaultRuntime.responses.pointer(handle)
}

// 返回响应字节长度
func ABIResponseLength(handle int64) int32 {
	return defaultRuntime.responses.length(handle)
}

// 释放响应句柄
func ABIResponseDrop(handle int64) {
	defaultRuntime.responses.drop(handle)
}
