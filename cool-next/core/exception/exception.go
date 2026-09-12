package exception

import (
	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
)

// 业务异常基类型
type BaseException struct {
	Name       string // 异常类型名称
	Code       int    // 业务错误码
	Message    string // 错误描述
	StatusCode int    // 响应状态码
	Cause      error  // 原始错误
}

// 返回异常消息
func (e *BaseException) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

// 返回原始错误
func (e *BaseException) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Cause
}

// 通用业务异常
func Comm(message string, statusCode ...int) error {
	return newException(CommException, CommFail, message, MsgCommFail, nil, statusCode...)
}

// 参数校验异常
func Validate(message string, statusCode ...int) error {
	return newException(ValidateException, ValidateFail, message, MsgValidateFail, nil, statusCode...)
}

// 核心服务异常
func Core(message string, statusCode ...int) error {
	return newException(CoreException, CoreFail, message, MsgCoreFail, nil, statusCode...)
}

// 包装通用业务异常
func WrapComm(cause error, message string, statusCode ...int) error {
	if cause == nil {
		return nil
	}

	return newException(CommException, CommFail, message, MsgCommFail, cause, statusCode...)
}

// 包装参数校验异常
func WrapValidate(cause error, message string, statusCode ...int) error {
	if cause == nil {
		return nil
	}

	return newException(ValidateException, ValidateFail, message, MsgValidateFail, cause, statusCode...)
}

// 包装核心服务异常
func WrapCore(cause error, message string, statusCode ...int) error {
	if cause == nil {
		return nil
	}

	return newException(CoreException, CoreFail, message, MsgCoreFail, cause, statusCode...)
}

// 创建新的业务异常
func newException(name string, code int, message, fallback string, cause error, statusCode ...int) error {
	if message == "" {
		message = fallback
	}

	exception := &BaseException{
		Name:       name,
		Code:       code,
		Message:    message,
		StatusCode: getStatusCode(statusCode...),
		Cause:      cause,
	}

	return gerror.WrapCodeSkip(gcode.CodeNil, 2, exception)
}

// 获取 HTTP 响应状态码
func getStatusCode(statusCode ...int) int {
	if len(statusCode) > 1 {
		panic("exception: statusCode 只能省略或传入一个值")
	}
	if len(statusCode) == 1 {
		return statusCode[0]
	}

	return 0
}
