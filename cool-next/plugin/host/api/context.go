package api

import (
	"context"
	"encoding/json"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/manager"
)

type emptyRequest struct{}

type contextResponse struct {
	TraceID  string          `json:"traceId"`
	Source   string          `json:"source"`
	Identity contextIdentity `json:"identity"`
}

type contextIdentity struct {
	Type string `json:"type"`
	ID   uint64 `json:"id"`
}

func contextOperations() []operation {
	return []operation{
		typedOperation("config.get", getConfig),
		typedOperation("context.get", getContext),
	}
}

func getConfig(ctx context.Context, _ int64, _ emptyRequest) (json.RawMessage, error) {
	metadata, err := pluginMetadata(ctx)
	if err != nil {
		return nil, err
	}

	return append(json.RawMessage(nil), metadata.Config...), nil
}

func getContext(ctx context.Context, _ int64, _ emptyRequest) (contextResponse, error) {
	invocation, exists := manager.InvocationFromContext(ctx)
	if !exists {
		return contextResponse{}, protocol.NewError(protocol.ErrorHostCallFailed, "插件调用上下文不可用")
	}

	return contextResponse{
		TraceID: app.TraceID(ctx),
		Source:  invocation.Source,
		Identity: contextIdentity{
			Type: invocation.Identity.Type,
			ID:   invocation.Identity.ID,
		},
	}, nil
}

func pluginMetadata(ctx context.Context) (manager.GenerationMetadata, error) {
	metadata, exists := manager.GenerationFromContext(ctx)
	if !exists || metadata.Key == "" {
		return manager.GenerationMetadata{}, protocol.NewError(protocol.ErrorHostCallFailed, "插件 generation 上下文不可用")
	}

	return metadata, nil
}

func pluginKey(ctx context.Context) (string, error) {
	metadata, err := pluginMetadata(ctx)
	if err != nil {
		return "", err
	}

	return metadata.Key, nil
}
