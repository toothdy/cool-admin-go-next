//go:build wasip1

package main

import (
	"unsafe"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk"
	"github.com/toothdy/tencent-cos-go/plugin"
)

func init() {
	sdk.Register(plugin.Plugin())
}

// WASM 导出函数由宿主调用，登记引用以避免静态分析误报。
var (
	_ = coolABIVersion
	_ = coolAlloc
	_ = coolFree
	_ = coolInit
	_ = coolInvoke
	_ = coolShutdown
	_ = coolResponsePointer
	_ = coolResponseLength
	_ = coolResponseDrop
)

//go:wasmexport cool_abi_version
func coolABIVersion() uint32 {
	return sdk.ABIVersion()
}

//go:wasmexport cool_alloc
func coolAlloc(size int32) unsafe.Pointer {
	return sdk.ABIAlloc(size)
}

//go:wasmexport cool_free
func coolFree(pointer unsafe.Pointer, size int32) {
	sdk.ABIFree(pointer, size)
}

//go:wasmexport cool_init
func coolInit(invocationID int64, configPointer unsafe.Pointer, configSize int32) int64 {
	return sdk.ABIInit(invocationID, configPointer, configSize)
}

//go:wasmexport cool_invoke
func coolInvoke(invocationID int64, methodPointer unsafe.Pointer, methodSize int32, inputPointer unsafe.Pointer, inputSize int32) int64 {
	return sdk.ABIInvoke(invocationID, methodPointer, methodSize, inputPointer, inputSize)
}

//go:wasmexport cool_shutdown
func coolShutdown(invocationID int64) int64 {
	return sdk.ABIShutdown(invocationID)
}

//go:wasmexport cool_response_pointer
func coolResponsePointer(handle int64) unsafe.Pointer {
	return sdk.ABIResponsePointer(handle)
}

//go:wasmexport cool_response_length
func coolResponseLength(handle int64) int32 {
	return sdk.ABIResponseLength(handle)
}

//go:wasmexport cool_response_drop
func coolResponseDrop(handle int64) {
	sdk.ABIResponseDrop(handle)
}

func main() {}
