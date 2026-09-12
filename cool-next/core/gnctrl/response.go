package gnctrl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/net/gtrace"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 统一 HTTP 响应
type Response struct {
	Code    int    `json:"code"`           // 业务状态码
	Message string `json:"message"`        // 响应消息
	Data    any    `json:"data,omitempty"` // 响应数据
}

// 原始 HTML 响应
type HTMLResponse string

// 文件响应处置方式
type FileDisposition string

const (
	FileDispositionInline     FileDisposition = "inline"     // 内联展示
	FileDispositionAttachment FileDisposition = "attachment" // 附件下载
)

// 响应层负责关闭的可定位文件内容
type FileContent interface {
	io.ReadSeekCloser
	Stat() (fs.FileInfo, error)
}

// 原始文件响应
type FileResponse struct {
	Content     FileContent
	Name        string
	ContentType string
	Disposition FileDisposition
	Headers     http.Header
}

// HTTP 错误日志器
type ErrorLogger interface {
	// 记录错误
	Error(context.Context, ...any)
}

// 创建可注入日志器的响应过滤中间件
func NewResponseMiddleware(logger ErrorLogger) ghttp.HandlerFunc {
	if logger == nil {
		logger = g.Log()
	}

	return func(request *ghttp.Request) {
		handleResponse(request, logger)
	}
}

// 过滤 Handler 响应和异常
func handleResponse(request *ghttp.Request, logger ErrorLogger) {
	if request == nil {
		return
	}
	defer func() {
		closeFileResponse(request.GetHandlerResponse())
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			err := panicError(recovered)
			request.SetError(err)
			writeError(request, logger, err, true)
		}
	}()
	if err := request.GetError(); err != nil {
		writeError(request, logger, err, false)
		return
	}

	request.Middleware.Next()
	if err := request.GetError(); err != nil {
		isPanic := request.Response.Status == http.StatusInternalServerError && request.Response.BufferLength() > 0
		writeError(request, logger, err, isPanic)
		return
	}
	if request.GetServeHandler() == nil || responseCommitted(request) || request.Response.BufferLength() > 0 {
		return
	}

	writeSuccess(request, logger)
}

// 转换处理器异常并保留堆栈
func panicError(recovered any) error {
	cause, ok := recovered.(error)
	if !ok {
		cause = errors.New(fmt.Sprint(recovered))
	}

	return gerror.WrapSkip(1, cause, "HTTP Handler panic")
}

// 写入成功响应
func writeSuccess(request *ghttp.Request, logger ErrorLogger) {
	if handled, err := writeRawResponse(request); handled {
		if err != nil {
			request.SetError(err)
			writeError(request, logger, err, false)
		}
		return
	}

	body, err := json.Marshal(Response{
		Code:    exception.Success,
		Message: exception.MsgSuccess,
		Data:    request.GetHandlerResponse(),
	})
	if err != nil {
		err = gerror.Wrap(err, "编码 HTTP 响应失败")
		request.SetError(err)
		writeError(request, logger, err, false)
		return
	}

	writeJSON(request, http.StatusOK, body)
}

// 写入明确声明的原始响应
func writeRawResponse(request *ghttp.Request) (bool, error) {
	switch result := request.GetHandlerResponse().(type) {
	case HTMLResponse:
		request.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		request.Response.Header().Del("Content-Length")
		request.Response.WriteHeader(http.StatusOK)
		request.Response.SetBuffer([]byte(result))

		return true, nil
	case FileResponse:
		return true, writeFileResponse(request, result)
	default:
		return false, nil
	}
}

// 写入调用方已决定元数据的文件响应
func writeFileResponse(request *ghttp.Request, result FileResponse) error {
	if !validFileContent(result.Content) || strings.TrimSpace(result.Name) == "" ||
		strings.TrimSpace(result.ContentType) == "" ||
		(result.Disposition != FileDispositionInline && result.Disposition != FileDispositionAttachment) {
		return exception.Core("文件响应无效")
	}
	if _, _, err := mime.ParseMediaType(result.ContentType); err != nil {
		return exception.Core("文件响应无效")
	}
	disposition := mime.FormatMediaType(string(result.Disposition), map[string]string{"filename": result.Name})
	if disposition == "" {
		return exception.Core("文件响应无效")
	}
	info, err := result.Content.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return exception.Core("文件响应无效")
	}

	for name, values := range result.Headers {
		for _, value := range values {
			request.Response.Header().Add(name, value)
		}
	}
	request.Response.Header().Set("Content-Type", result.ContentType)
	request.Response.Header().Set("Content-Disposition", disposition)
	request.Response.Header().Del("Content-Length")
	request.Response.ServeContent(result.Name, info.ModTime(), result.Content)

	return nil
}

// 判断文件内容接口是否持有有效实例
func validFileContent(content FileContent) bool {
	if content == nil {
		return false
	}
	value := reflect.ValueOf(content)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

// 关闭 Handler 交给响应层的文件句柄
func closeFileResponse(response any) {
	result, ok := response.(FileResponse)
	if !ok || !validFileContent(result.Content) {
		return
	}
	_ = result.Content.Close()
}

// 写入安全错误响应
func writeError(request *ghttp.Request, logger ErrorLogger, err error, isPanic bool) {
	result := exception.Resolve(err)
	if isPanic {
		result = exception.Result{
			Code:       exception.CommFail,
			Message:    exception.MsgCommFail,
			StatusCode: http.StatusInternalServerError,
		}
	}
	logErr := err
	if !gerror.HasStack(logErr) {
		logErr = gerror.Wrap(logErr, "HTTP Handler error")
	}
	logResponseError(request, logger, logErr)
	request.SetError(nil)
	request.Response.ClearBuffer()
	if responseCommitted(request) {
		return
	}

	body, marshalErr := json.Marshal(Response{Code: result.Code, Message: result.Message})
	if marshalErr != nil {
		logger.Error(request.Context(), marshalErr)
		body = []byte(`{"code":1001,"message":"comm fail"}`)
		result.StatusCode = http.StatusInternalServerError
	}
	writeJSON(request, result.StatusCode, body)
}

// 写入缓冲 JSON 响应
func writeJSON(request *ghttp.Request, statusCode int, body []byte) {
	request.Response.Header().Set("Content-Type", "application/json")
	request.Response.Header().Del("Content-Length")
	request.Response.WriteHeader(statusCode)
	request.Response.SetBuffer(body)
}

// 判断响应是否已经发送
func responseCommitted(request *ghttp.Request) bool {
	return request.Response.BytesWritten() > 0 || request.Response.IsHeaderWrote() || request.Response.IsHijacked()
}

// 记录脱敏错误和 Trace ID
func logResponseError(request *ghttp.Request, logger ErrorLogger, err error) {
	logger.Error(
		request.Context(),
		fmt.Sprintf(
			"trace_id=%q error=%s",
			gtrace.GetTraceID(request.Context()),
			exception.LogText(err),
		),
	)
}
