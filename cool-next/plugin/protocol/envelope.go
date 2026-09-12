package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// 插件稳定错误码
type ErrorCode string

const (
	ErrorNotFound          ErrorCode = "PLUGIN_NOT_FOUND"
	ErrorDisabled          ErrorCode = "PLUGIN_DISABLED"
	ErrorMethodNotFound    ErrorCode = "PLUGIN_METHOD_NOT_FOUND"
	ErrorInvalidInput      ErrorCode = "PLUGIN_INVALID_INPUT"
	ErrorInvalidOutput     ErrorCode = "PLUGIN_INVALID_OUTPUT"
	ErrorABIUnsupported    ErrorCode = "PLUGIN_ABI_UNSUPPORTED"
	ErrorInitFailed        ErrorCode = "PLUGIN_INIT_FAILED"
	ErrorTimeout           ErrorCode = "PLUGIN_TIMEOUT"
	ErrorTrap              ErrorCode = "PLUGIN_TRAP"
	ErrorResourceExhausted ErrorCode = "PLUGIN_RESOURCE_EXHAUSTED"
	ErrorHostCallFailed    ErrorCode = "PLUGIN_HOST_CALL_FAILED"
	ErrorCallCycle         ErrorCode = "PLUGIN_CALL_CYCLE"
	ErrorCallDepthExceeded ErrorCode = "PLUGIN_CALL_DEPTH_EXCEEDED"
)

// 插件调用错误
type PluginError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	cause   error
}

// 返回插件错误文本
func (pluginError *PluginError) Error() string {
	if pluginError == nil {
		return ""
	}
	if pluginError.Code == "" {
		return pluginError.Message
	}
	return fmt.Sprintf("%s: %s", pluginError.Code, pluginError.Message)
}

// 返回底层错误
func (pluginError *PluginError) Unwrap() error {
	if pluginError == nil {
		return nil
	}
	return pluginError.cause
}

// 创建插件错误
func NewError(code ErrorCode, message string) *PluginError {
	return &PluginError{Code: code, Message: message}
}

// 包装插件错误
func WrapError(code ErrorCode, message string, cause error) *PluginError {
	return &PluginError{Code: code, Message: message, cause: cause}
}

type envelope struct {
	OK    *bool           `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *PluginError    `json:"error,omitempty"`
}

// 编码成功 Envelope
func EncodeSuccess(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	if !json.Valid(data) {
		return nil, errors.New("成功响应 data 不是合法 JSON")
	}
	ok := true
	result, err := json.Marshal(envelope{OK: &ok, Data: data})
	if err != nil {
		return nil, fmt.Errorf("编码成功响应失败: %w", err)
	}

	return result, nil
}

// 编码失败 Envelope
func EncodeFailure(code ErrorCode, message string) (json.RawMessage, error) {
	if strings.TrimSpace(string(code)) == "" {
		return nil, errors.New("失败响应错误码不能为空")
	}
	if strings.TrimSpace(message) == "" {
		return nil, errors.New("失败响应错误消息不能为空")
	}
	ok := false
	result, err := json.Marshal(envelope{
		OK:    &ok,
		Error: NewError(code, message),
	})
	if err != nil {
		return nil, fmt.Errorf("编码失败响应失败: %w", err)
	}

	return result, nil
}

// 解码 Envelope，失败响应返回 PluginError
func Decode(payload json.RawMessage) (json.RawMessage, error) {
	var value envelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("解码插件响应失败: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	if value.OK == nil {
		return nil, errors.New("插件响应缺少 ok")
	}
	if *value.OK {
		if value.Error != nil {
			return nil, errors.New("成功响应不能包含 error")
		}
		if len(value.Data) == 0 || !json.Valid(value.Data) {
			return nil, errors.New("成功响应缺少合法 data")
		}
		return append(json.RawMessage(nil), value.Data...), nil
	}
	if len(value.Data) > 0 {
		return nil, errors.New("失败响应不能包含 data")
	}
	if value.Error == nil || strings.TrimSpace(string(value.Error.Code)) == "" || strings.TrimSpace(value.Error.Message) == "" {
		return nil, errors.New("失败响应缺少合法 error")
	}

	return nil, value.Error
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("插件响应包含多余 JSON")
		}
		return fmt.Errorf("读取插件响应结尾失败: %w", err)
	}

	return nil
}
