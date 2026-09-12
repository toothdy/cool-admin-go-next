package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

type invocationKey struct{}

type hostCaller func(context.Context, int64, string, json.RawMessage) (json.RawMessage, error)

type invocationContext struct {
	id     int64
	config json.RawMessage
	call   hostCaller
}

// 返回当前插件调用 ID
func InvocationID(ctx context.Context) (int64, bool) {
	invocation, exists := invocationFrom(ctx)
	if !exists {
		return 0, false
	}

	return invocation.id, true
}

// 解码当前插件配置
func Config[Value any](ctx context.Context) (Value, error) {
	var result Value
	invocation, exists := invocationFrom(ctx)
	if !exists {
		return result, errors.New("当前 Context 不包含插件调用")
	}
	if err := json.Unmarshal(invocation.config, &result); err != nil {
		return result, protocol.WrapError(protocol.ErrorInvalidInput, "插件配置无效", err)
	}

	return result, nil
}

// 调用宿主显式开放的操作
func HostCall(ctx context.Context, operation string, input json.RawMessage) (json.RawMessage, error) {
	invocation, exists := invocationFrom(ctx)
	if !exists || invocation.call == nil {
		return nil, protocol.NewError(protocol.ErrorHostCallFailed, "当前 Context 不支持宿主调用")
	}
	if strings.TrimSpace(operation) == "" || operation != strings.TrimSpace(operation) {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "宿主操作名无效")
	}
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	if !json.Valid(input) {
		return nil, protocol.NewError(protocol.ErrorInvalidInput, "宿主调用参数不是合法 JSON")
	}
	result, err := invocation.call(ctx, invocation.id, operation, input)
	if err != nil {
		return nil, err
	}

	return append(json.RawMessage(nil), result...), nil
}

func invocationFrom(ctx context.Context) (invocationContext, bool) {
	if ctx == nil {
		return invocationContext{}, false
	}
	value, exists := ctx.Value(invocationKey{}).(invocationContext)

	return value, exists
}

func withInvocation(ctx context.Context, id int64, config json.RawMessage, call hostCaller) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationKey{}, invocationContext{
		id:     id,
		config: append(json.RawMessage(nil), config...),
		call:   call,
	})
}
