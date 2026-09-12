package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

const (
	maxLogMessageBytes = 8 * 1024
	maxLogFieldsBytes  = 64 * 1024
)

type logRequest struct {
	Level   string          `json:"level"`
	Message string          `json:"message"`
	Fields  json.RawMessage `json:"fields,omitempty"`
}

func logOperations(config Config, dependencies Dependencies) []operation {
	write := logWriter(dependencies.Log)

	return []operation{
		typedOperation("log.write", func(
			ctx context.Context,
			invocationID int64,
			request logRequest,
		) (emptyRequest, error) {
			return emptyRequest{}, writePluginLog(ctx, invocationID, request, config, write)
		}),
	}
}

func writePluginLog(
	ctx context.Context,
	invocationID int64,
	request logRequest,
	config Config,
	write func(context.Context, LogRecord),
) error {
	if request.Level != "debug" && request.Level != "info" && request.Level != "warn" && request.Level != "error" {
		return protocol.NewError(protocol.ErrorInvalidInput, "日志级别无效")
	}
	if strings.TrimSpace(request.Message) == "" {
		return protocol.NewError(protocol.ErrorInvalidInput, "日志消息不能为空")
	}
	if len(request.Message) > maxLogMessageBytes || uint64(len(request.Message)) > uint64(config.MaxPayloadBytes) {
		return protocol.NewError(protocol.ErrorResourceExhausted, "日志消息过长")
	}
	fields := request.Fields
	if len(fields) == 0 {
		fields = json.RawMessage(`{}`)
	}
	var values map[string]json.RawMessage
	if err := decodeStrictJSON(fields, &values); err != nil {
		return protocol.WrapError(protocol.ErrorInvalidInput, "日志字段必须是 JSON object", err)
	}
	fields, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(fields) > maxLogFieldsBytes || uint64(len(fields)) > uint64(config.MaxPayloadBytes) {
		return protocol.NewError(protocol.ErrorResourceExhausted, "日志字段过大")
	}
	metadata, err := pluginMetadata(ctx)
	if err != nil {
		return err
	}
	write(ctx, LogRecord{
		Level:         request.Level,
		Message:       request.Message,
		PluginKey:     metadata.Key,
		PluginVersion: metadata.Version,
		InvocationID:  invocationID,
		Fields:        append(json.RawMessage(nil), fields...),
	})

	return nil
}

func logWriter(write func(context.Context, LogRecord)) func(context.Context, LogRecord) {
	if write != nil {
		return write
	}

	return writeDefaultLog
}

func writeDefaultLog(ctx context.Context, record LogRecord) {
	values := []any{
		"pluginKey", record.PluginKey,
		"pluginVersion", record.PluginVersion,
		"invocationID", record.InvocationID,
	}
	if len(record.Fields) > 0 {
		values = append(values, "fields", string(record.Fields))
	}
	if record.Cause != nil {
		values = append(values, exception.LogText(record.Cause))
	}
	values = append([]any{record.Message}, values...)
	switch record.Level {
	case "debug":
		g.Log().Debug(ctx, values...)
	case "warn":
		g.Log().Warning(ctx, values...)
	case "error":
		g.Log().Error(ctx, values...)
	default:
		g.Log().Info(ctx, values...)
	}
}
