package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

var (
	errHTTPRedirectURL = errors.New("HTTP 重定向地址无效")
	errHTTPRedirects   = errors.New("HTTP 重定向次数超限")
)

var deniedHTTPHeaders = map[string]struct{}{
	"connection":          {},
	"content-length":      {},
	"cookie":              {},
	"cookie2":             {},
	"host":                {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

type httpDoRequest struct {
	Method   string      `json:"method"`
	URL      string      `json:"url"`
	Headers  http.Header `json:"headers,omitempty"`
	Body     []byte      `json:"body,omitempty"`
	BodyFile string      `json:"bodyFile,omitempty"`
}

type httpDoResponse struct {
	StatusCode int         `json:"statusCode"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

type httpHandler struct {
	client           *http.Client
	files            *fileHandler
	maxRequestBytes  int64
	maxResponseBytes int64
}

func httpOperations(config Config) ([]operation, error) {
	handler, err := newHTTPHandler(config)
	if err != nil {
		return nil, err
	}

	return []operation{typedOperation("http.do", handler.handle)}, nil
}

func newHTTPHandler(config Config) (*httpHandler, error) {
	if config.HTTPTimeout <= 0 {
		return nil, errors.New("HTTP 超时必须大于 0")
	}
	if config.MaxHTTPRedirects <= 0 {
		return nil, errors.New("HTTP 重定向上限必须大于 0")
	}
	if config.MaxPayloadBytes == 0 {
		return nil, errors.New("宿主调用载荷上限必须大于 0")
	}
	if config.MaxHTTPResponseBytes <= 0 || config.MaxHTTPResponseBytes >= math.MaxInt64 {
		return nil, errors.New("HTTP 响应大小上限无效")
	}

	// 独立 Transport 避免继承宿主的代理和 TLS 凭据
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: int64(config.MaxPayloadBytes),
	}

	var (
		files *fileHandler
		err   error
	)
	if strings.TrimSpace(config.DataRoot) != "" {
		files, err = newFileHandler(config)
		if err != nil {
			return nil, err
		}
	}
	handler := &httpHandler{
		files:            files,
		maxRequestBytes:  int64(config.MaxPayloadBytes),
		maxResponseBytes: config.MaxHTTPResponseBytes,
	}
	handler.client = &http.Client{
		Transport: transport,
		Timeout:   config.HTTPTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= config.MaxHTTPRedirects {
				return errHTTPRedirects
			}
			if err := validateHTTPURL(request.URL); err != nil {
				return fmt.Errorf("%w: %v", errHTTPRedirectURL, err)
			}
			return nil
		},
	}

	return handler, nil
}

func (handler *httpHandler) handle(ctx context.Context, _ int64, requestInput httpDoRequest) (httpDoResponse, error) {
	if int64(len(requestInput.Body)) > handler.maxRequestBytes {
		return httpDoResponse{}, protocol.NewError(protocol.ErrorResourceExhausted, "HTTP 请求体超限")
	}
	if len(requestInput.Body) > 0 && requestInput.BodyFile != "" {
		return httpDoResponse{}, protocol.NewError(protocol.ErrorInvalidInput, "HTTP body 与 bodyFile 不能同时设置")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestBody := io.Reader(bytes.NewReader(requestInput.Body))
	contentLength := int64(len(requestInput.Body))
	closeBody := func() {}
	if requestInput.BodyFile != "" {
		var err error
		requestBody, contentLength, closeBody, err = handler.openBodyFile(ctx, requestInput.BodyFile)
		if err != nil {
			return httpDoResponse{}, err
		}
		defer closeBody()
	}
	request, err := http.NewRequestWithContext(ctx, requestInput.Method, requestInput.URL, requestBody)
	if err != nil {
		return httpDoResponse{}, protocol.WrapError(protocol.ErrorInvalidInput, "HTTP 请求地址或方法无效", err)
	}
	request.ContentLength = contentLength
	if err := validateHTTPURL(request.URL); err != nil {
		return httpDoResponse{}, protocol.WrapError(protocol.ErrorInvalidInput, "HTTP 请求地址无效", err)
	}
	if err := copyHTTPHeaders(request.Header, requestInput.Headers); err != nil {
		return httpDoResponse{}, protocol.WrapError(protocol.ErrorInvalidInput, "HTTP 请求头无效", err)
	}

	response, err := handler.client.Do(request)
	if err != nil {
		return httpDoResponse{}, classifyHTTPError(ctx, err)
	}
	defer response.Body.Close()
	if response.ContentLength > handler.maxResponseBytes {
		return httpDoResponse{}, protocol.NewError(protocol.ErrorResourceExhausted, "HTTP 响应体超限")
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, handler.maxResponseBytes+1))
	if err != nil {
		return httpDoResponse{}, classifyHTTPError(ctx, err)
	}
	if int64(len(responseBody)) > handler.maxResponseBytes {
		return httpDoResponse{}, protocol.NewError(protocol.ErrorResourceExhausted, "HTTP 响应体超限")
	}

	return httpDoResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Header.Clone(),
		Body:       responseBody,
	}, nil
}

func (handler *httpHandler) openBodyFile(ctx context.Context, value string) (io.Reader, int64, func(), error) {
	key, err := pluginKey(ctx)
	if err != nil {
		return nil, 0, nil, err
	}

	return handler.openBodyFileForKey(ctx, key, value)
}

func (handler *httpHandler) openBodyFileForKey(
	ctx context.Context,
	key string,
	value string,
) (io.Reader, int64, func(), error) {
	if handler.files == nil {
		return nil, 0, nil, protocol.NewError(protocol.ErrorHostCallFailed, "HTTP bodyFile 不可用")
	}
	relative, exists := strings.CutPrefix(value, "/data/")
	if !exists {
		return nil, 0, nil, protocol.NewError(protocol.ErrorInvalidInput, "HTTP bodyFile 必须位于 /data")
	}
	relative, err := normalizeFilePath(relative, false)
	if err != nil {
		return nil, 0, nil, protocol.WrapError(protocol.ErrorInvalidInput, "HTTP bodyFile 路径无效", err)
	}
	root, err := handler.files.openPluginRoot(key)
	if err != nil {
		return nil, 0, nil, fileError(ctx, "HTTP bodyFile 目录不可用", err)
	}
	info, err := checkFilePath(root, relative)
	if err != nil || !info.Mode().IsRegular() {
		root.Close()
		if err == nil {
			err = errUnsafeFilePath
		}
		return nil, 0, nil, fileError(ctx, "HTTP bodyFile 路径无效", err)
	}
	file, err := root.Open(relative)
	if err != nil {
		root.Close()
		return nil, 0, nil, fileError(ctx, "打开 HTTP bodyFile 失败", err)
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
		file.Close()
		root.Close()
		if err == nil {
			err = errUnsafeFilePath
		}
		return nil, 0, nil, fileError(ctx, "HTTP bodyFile 路径无效", err)
	}

	return file, openedInfo.Size(), func() {
		_ = file.Close()
		_ = root.Close()
	}, nil
}

func validateHTTPURL(value *url.URL) error {
	if value == nil || value.Host == "" {
		return errors.New("HTTP 地址必须包含主机")
	}
	if value.User != nil {
		return errors.New("HTTP 地址不能包含用户信息")
	}
	switch strings.ToLower(value.Scheme) {
	case "http", "https":
		return nil
	default:
		return errors.New("HTTP 地址只允许 http 或 https")
	}
}

func copyHTTPHeaders(target, source http.Header) error {
	for name, values := range source {
		if !isHTTPHeaderName(name) {
			return fmt.Errorf("请求头名无效: %q", name)
		}
		if _, denied := deniedHTTPHeaders[strings.ToLower(name)]; denied {
			return fmt.Errorf("不允许设置请求头: %s", name)
		}
		for _, value := range values {
			if !isHTTPHeaderValue(value) {
				return fmt.Errorf("请求头值无效: %s", name)
			}
			target.Add(name, value)
		}
	}

	return nil
}

func isHTTPHeaderName(value string) bool {
	if value == "" {
		return false
	}
	const token = "!#$%&'*+-.^_`|~"
	for index := 0; index < len(value); index++ {
		current := value[index]
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			strings.IndexByte(token, current) >= 0 {
			continue
		}
		return false
	}

	return true
}

func isHTTPHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current == '\t' || current >= ' ' && current != 0x7f {
			continue
		}
		return false
	}

	return true
}

func classifyHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, errHTTPRedirectURL) {
		return protocol.WrapError(protocol.ErrorInvalidInput, "HTTP 重定向地址无效", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return protocol.WrapError(protocol.ErrorTimeout, "HTTP 请求超时", err)
	}
	var networkError interface{ Timeout() bool }
	if errors.As(err, &networkError) && networkError.Timeout() {
		return protocol.WrapError(protocol.ErrorTimeout, "HTTP 请求超时", err)
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return protocol.WrapError(protocol.ErrorTimeout, "HTTP 请求超时", err)
	}
	if errors.Is(err, errHTTPRedirects) {
		return protocol.WrapError(protocol.ErrorHostCallFailed, "HTTP 重定向次数超限", err)
	}
	if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return protocol.WrapError(protocol.ErrorHostCallFailed, "HTTP 请求已取消", err)
	}

	return protocol.WrapError(protocol.ErrorHostCallFailed, "HTTP 请求失败", err)
}
