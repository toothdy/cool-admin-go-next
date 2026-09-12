package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

//go:wasmimport cool_host call
func wasmHostCall(invocationID int64, operationPointer unsafe.Pointer, operationSize int32, inputPointer unsafe.Pointer, inputSize int32) int64

//go:wasmimport cool_host response_length
func wasmHostResponseLength(handle int64) int32

//go:wasmimport cool_host response_read
func wasmHostResponseRead(handle int64, targetPointer unsafe.Pointer, targetSize int32) int32

//go:wasmimport cool_host response_drop
func wasmHostResponseDrop(handle int64)

func callHostABI(_ context.Context, invocationID int64, operation string, input json.RawMessage) (json.RawMessage, error) {
	operationBytes := []byte(operation)
	handle := wasmHostCall(
		invocationID,
		bytePointer(operationBytes),
		int32(len(operationBytes)),
		bytePointer(input),
		int32(len(input)),
	)
	if handle <= 0 {
		return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主调用未返回有效响应")
	}
	defer wasmHostResponseDrop(handle)

	size := wasmHostResponseLength(handle)
	if size < 0 {
		return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主响应句柄无效")
	}
	payload := make([]byte, size)
	if size > 0 {
		read := wasmHostResponseRead(handle, bytePointer(payload), size)
		if read != size {
			return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主响应读取不完整")
		}
	}
	result, err := protocol.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("解码宿主响应失败: %w", err)
	}

	return result, nil
}

func bytePointer(value []byte) unsafe.Pointer {
	if len(value) == 0 {
		return nil
	}

	return unsafe.Pointer(&value[0])
}
