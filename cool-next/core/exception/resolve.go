package exception

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"
)

const redacted = "[REDACTED]"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:amqps?|mongodb(?:\+srv)?|mssql|mysql|postgres(?:ql)?|redis(?:s)?|sqlserver)://[^\s"'<>]+`),
	regexp.MustCompile(`(?i)\bfile:(?://)?[^\s"'<>]+`),
	regexp.MustCompile(`(?i)\b[^\s:@/]+:[^\s@/]+@(?:tcp|unix)\([^)]*\)/[^\s"'<>]*`),
	regexp.MustCompile(`(?i)(["']?(?:access[_-]?token|refresh[_-]?token|token|password|passwd|pwd|cookie|authorization|secret)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,;]+|[^\s,;]+)`),
	regexp.MustCompile(`(?i)(["']?(?:host|hostname|server|addr|address|port|user|username|user\s+id|database|dbname|dsn|data[_\s-]?source)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
}

// 协议无关的安全异常结果
type Result struct {
	Code       int    // 业务错误码
	Message    string // 对外安全消息
	StatusCode int    // HTTP 响应状态码
}

// 解析对外安全异常结果
func Resolve(err error) Result {
	if err == nil {
		return Result{Code: Success, Message: MsgSuccess, StatusCode: http.StatusOK}
	}

	var coolError *BaseException
	if !errors.As(err, &coolError) || coolError == nil || !isValidStatusCode(coolError.StatusCode) {
		return unknownResult()
	}

	statusCode := coolError.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	switch {
	case coolError.Name == CommException && coolError.Code == CommFail:
		return Result{Code: CommFail, Message: safeMessage(coolError.Message, MsgCommFail), StatusCode: statusCode}
	case coolError.Name == ValidateException && coolError.Code == ValidateFail:
		return Result{Code: ValidateFail, Message: safeMessage(coolError.Message, MsgValidateFail), StatusCode: statusCode}
	case coolError.Name == CoreException && coolError.Code == CoreFail:
		return Result{Code: CoreFail, Message: MsgCoreFail, StatusCode: statusCode}
	default:
		return unknownResult()
	}
}

// 生成内部日志的脱敏错误文本
func LogText(err error) string {
	if err == nil {
		return ""
	}

	stack := gerror.Stack(err)
	var coolError *BaseException
	if errors.As(err, &coolError) && coolError != nil && coolError.Cause != nil {
		causeStack := gerror.Stack(coolError.Cause)
		if causeStack != "" && !strings.Contains(stack, causeStack) {
			stack += "\ncause:\n" + causeStack
		}
	}

	return redact(stack)
}

// 校验可携带统一 JSON 响应体的状态码
func isValidStatusCode(statusCode int) bool {
	if statusCode == 0 {
		return true
	}

	return statusCode >= 200 && statusCode <= 599 &&
		statusCode != http.StatusNoContent &&
		statusCode != http.StatusResetContent &&
		statusCode != http.StatusNotModified
}

// 返回未知错误的安全结果
func unknownResult() Result {
	return Result{Code: CommFail, Message: MsgCommFail, StatusCode: http.StatusInternalServerError}
}

// 生成脱敏后的非空业务消息
func safeMessage(message, fallback string) string {
	if message == "" {
		message = fallback
	}

	return redact(message)
}

// 隐藏凭据和常见连接信息
func redact(value string) string {
	for index, pattern := range redactionPatterns {
		replacement := redacted
		if index == 3 || index == 4 {
			replacement = "${1}" + redacted
		}
		value = pattern.ReplaceAllString(value, replacement)
	}

	return value
}
