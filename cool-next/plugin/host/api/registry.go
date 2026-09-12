package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// Host API 的资源边界配置
type Config struct {
	MaxPayloadBytes      uint32
	HTTPTimeout          time.Duration
	MaxHTTPRedirects     int
	MaxHTTPResponseBytes int64
	DataRoot             string
}

// Host API JSON 调用处理器
type Handler func(context.Context, int64, json.RawMessage) (json.RawMessage, error)

// 插件间调用接口
type Invoker interface {
	InvokeJSON(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}

// 宿主显式开放的业务能力
type Adapter func(context.Context, json.RawMessage) (json.RawMessage, error)

// 写入宿主日志的结构化记录
type LogRecord struct {
	Level         string
	Message       string
	PluginKey     string
	PluginVersion string
	InvocationID  int64
	Fields        json.RawMessage
	Cause         error
}

// Host API 使用的显式依赖
type Dependencies struct {
	Invoker  Invoker
	Adapters map[string]Adapter
	Log      func(context.Context, LogRecord)
	Cache    CacheStore
}

type operation struct {
	name   string
	handle Handler
}

// 固定 Host API 操作表
type Registry struct {
	operations      map[string]Handler
	maxPayloadBytes uint32
	log             func(context.Context, LogRecord)
}

// 创建包含全部内建操作的 Host API 注册表
func New(config Config, dependencies Dependencies) (*Registry, error) {
	if dependencies.Invoker == nil {
		return nil, errors.New("插件调用器不能为空")
	}
	for name, adapter := range dependencies.Adapters {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || adapter == nil {
			return nil, fmt.Errorf("宿主适配器定义无效: %q", name)
		}
	}
	http, err := httpOperations(config)
	if err != nil {
		return nil, err
	}
	files, err := fileOperations(config, dependencies)
	if err != nil {
		return nil, err
	}
	operations := append(contextOperations(), logOperations(config, dependencies)...)
	operations = append(operations, pluginOperations(dependencies)...)
	operations = append(operations, cacheOperations(config, dependencies)...)
	operations = append(operations, files...)
	operations = append(operations, http...)

	return newRegistry(config, dependencies, operations...)
}

func newRegistry(config Config, dependencies Dependencies, extras ...operation) (*Registry, error) {
	if config.MaxPayloadBytes == 0 {
		return nil, errors.New("Host API 载荷上限必须大于 0")
	}
	registry := &Registry{
		operations:      make(map[string]Handler, len(extras)),
		maxPayloadBytes: config.MaxPayloadBytes,
		log:             logWriter(dependencies.Log),
	}
	for _, current := range extras {
		if strings.TrimSpace(current.name) == "" || current.name != strings.TrimSpace(current.name) || current.handle == nil {
			return nil, fmt.Errorf("Host API 操作定义无效: %q", current.name)
		}
		if _, exists := registry.operations[current.name]; exists {
			return nil, fmt.Errorf("Host API 操作重复: %s", current.name)
		}
		registry.operations[current.name] = current.handle
	}

	return registry, nil
}

// 分发 Host API 调用并强制执行统一资源边界
func (registry *Registry) Handle(
	ctx context.Context,
	invocationID int64,
	name string,
	input json.RawMessage,
) (json.RawMessage, error) {
	if registry == nil {
		return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主操作注册表不可用")
	}
	if uint64(len(input)) > uint64(registry.maxPayloadBytes) {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "宿主调用载荷超限")
	}
	handle, exists := registry.operations[name]
	if !exists {
		return nil, protocol.NewError(protocol.ErrorHostCallFailed, "宿主操作未注册")
	}
	result, err := handle(ctx, invocationID, input)
	if err != nil {
		var pluginError *protocol.PluginError
		if errors.As(err, &pluginError) && pluginError != nil {
			if cause := errors.Unwrap(pluginError); cause != nil {
				registry.logCause(ctx, invocationID, cause)
			}
			return nil, err
		}
		registry.logCause(ctx, invocationID, err)

		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "宿主操作执行失败", err)
	}
	if !json.Valid(result) {
		cause := errors.New("宿主操作返回了非法 JSON")
		registry.logCause(ctx, invocationID, cause)

		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "宿主操作执行失败", cause)
	}
	if uint64(len(result)) > uint64(registry.maxPayloadBytes) {
		return nil, protocol.NewError(protocol.ErrorResourceExhausted, "宿主响应载荷超限")
	}

	return append(json.RawMessage(nil), result...), nil
}

func typedOperation[Request, Response any](
	name string,
	handle func(context.Context, int64, Request) (Response, error),
) operation {
	return operation{
		name: name,
		handle: func(ctx context.Context, invocationID int64, input json.RawMessage) (json.RawMessage, error) {
			var request Request
			if err := decodeStrictJSON(input, &request); err != nil {
				return nil, protocol.WrapError(protocol.ErrorInvalidInput, "宿主调用参数无效", err)
			}
			response, err := handle(ctx, invocationID, request)
			if err != nil {
				return nil, err
			}
			result, err := json.Marshal(response)
			if err != nil {
				return nil, fmt.Errorf("编码宿主响应失败: %w", err)
			}

			return result, nil
		},
	}
}

func decodeStrictJSON(input json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("请求必须是 JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("请求包含多余 JSON")
		}
		return fmt.Errorf("读取请求结尾失败: %w", err)
	}

	return nil
}

func (registry *Registry) logCause(ctx context.Context, invocationID int64, cause error) {
	if registry.log == nil || cause == nil {
		return
	}
	record := LogRecord{
		Level:        "error",
		Message:      "插件 Host API 调用失败",
		InvocationID: invocationID,
		Cause:        cause,
	}
	if metadata, err := pluginMetadata(ctx); err == nil {
		record.PluginKey = metadata.Key
		record.PluginVersion = metadata.Version
	}
	registry.log(ctx, record)
}
