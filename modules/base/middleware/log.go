package middleware

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

const maxLogParams = 8 << 10

// 后台业务操作日志
type LogHandler struct {
	log *service.LogService
}

// 操作日志中间件
func NewLogHandler(logService *service.LogService) *LogHandler {
	return &LogHandler{log: logService}
}

// 记录允许范围内的后台请求
func (handler *LogHandler) Handle(request *ghttp.Request) {
	if request == nil {
		return
	}
	path := request.URL.Path
	shouldRecord := isBusinessAdminPath(path)
	var params map[string]any
	if shouldRecord {
		params = sanitizeParams(request.GetMap())
	}
	request.Middleware.Next()
	if !shouldRecord || handler == nil || handler.log == nil {
		return
	}
	var userID *uint64
	if identity, err := auth.Admin(request.Context()); err == nil {
		userID = &identity.UserID
	}
	if err := handler.log.Record(request.Context(), service.LogRecord{
		UserID: userID,
		Action: path,
		IP:     request.GetClientIp(),
		Params: params,
	}); err != nil {
		g.Log().Error(request.Context(), "写入操作日志失败", err)
	}
}

func isBusinessAdminPath(path string) bool {
	if !strings.HasPrefix(path, "/admin/") {
		return false
	}
	for _, excluded := range []string{
		"/admin/base/open/",
		"/admin/base/comm/person/",
		"/admin/base/comm/permmenu/",
		"/admin/base/comm/program",
		"/admin/base/open/eps",
		"/admin/base/open/captcha",
		"/admin/health",
		"/admin/health/",
	} {
		if path == strings.TrimSuffix(excluded, "/") || strings.HasPrefix(path, excluded) {
			return false
		}
	}
	return true
}

func sanitizeParams(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = sanitizeValue(key, value)
	}
	return truncateParams(result)
}

func sanitizeValue(key string, value any) any {
	if isSensitiveKey(key) || isFileValue(value) {
		return "[REDACTED]"
	}
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for childKey, childValue := range current {
			result[childKey] = sanitizeValue(childKey, childValue)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, childValue := range current {
			result[index] = sanitizeValue(key, childValue)
		}
		return result
	default:
		return value
	}
}

func truncateParams(input map[string]any) map[string]any {
	encoded, err := json.Marshal(input)
	if err == nil && len(encoded) <= maxLogParams {
		return input
	}
	preview := string(encoded)
	if len(preview) > 1024 {
		preview = strings.ToValidUTF8(preview[:1024], "")
	}
	return map[string]any{
		"_truncated": true,
		"_preview":   preview,
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.Map(func(value rune) rune {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' {
			return value
		}
		return -1
	}, key))
	for _, sensitive := range []string{
		"password", "oldpassword", "token", "refreshtoken", "authorization", "verifycode", "captchaid", "captcha", "file", "content",
	} {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	return false
}

func isFileValue(value any) bool {
	if value == nil {
		return false
	}
	typeName := reflect.TypeOf(value).String()
	return strings.Contains(strings.ToLower(typeName), "uploadfile") || strings.Contains(strings.ToLower(typeName), "multipart.file")
}
