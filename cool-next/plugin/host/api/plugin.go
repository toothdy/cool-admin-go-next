package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

type pluginCallRequest struct {
	Target string          `json:"target"`
	Method string          `json:"method"`
	Input  json.RawMessage `json:"input"`
}

type hostCallRequest struct {
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
}

func pluginOperations(dependencies Dependencies) []operation {
	adapters := make(map[string]Adapter, len(dependencies.Adapters))
	for name, adapter := range dependencies.Adapters {
		adapters[name] = adapter
	}

	return []operation{
		typedOperation("plugin.call", func(
			ctx context.Context,
			_ int64,
			request pluginCallRequest,
		) (json.RawMessage, error) {
			if request.Target == "" || request.Target != strings.TrimSpace(request.Target) ||
				request.Method == "" || request.Method != strings.TrimSpace(request.Method) || len(request.Input) == 0 {
				return nil, protocol.NewError(protocol.ErrorInvalidInput, "插件调用参数无效")
			}

			return dependencies.Invoker.InvokeJSON(ctx, request.Target, request.Method, request.Input)
		}),
		typedOperation("host.call", func(
			ctx context.Context,
			_ int64,
			request hostCallRequest,
		) (json.RawMessage, error) {
			if request.Operation == "" || request.Operation != strings.TrimSpace(request.Operation) || len(request.Input) == 0 {
				return nil, protocol.NewError(protocol.ErrorInvalidInput, "宿主适配器调用参数无效")
			}
			adapter, exists := adapters[request.Operation]
			if !exists {
				return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主适配器未注册")
			}

			return adapter(ctx, request.Input)
		}),
	}
}
