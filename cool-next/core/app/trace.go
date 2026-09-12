package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

type requestContextKey struct{}

// 协议无关的请求信息
type requestInfo struct {
	traceID string
}

// 生成协议无关 Trace ID
func NewTraceID() (string, error) {
	content := make([]byte, 16)
	rand.Read(content)

	return hex.EncodeToString(content), nil
}

// 写入协议无关 Trace ID
func WithTraceID(ctx context.Context, traceID string) (context.Context, error) {
	if ctx == nil {
		return nil, exception.Core("请求上下文不能为空")
	}
	if !validTraceID(traceID) {
		return ctx, exception.Core("Trace ID 无效")
	}
	info := traceInfo(ctx)
	info.traceID = traceID

	return context.WithValue(ctx, requestContextKey{}, info), nil
}

// 返回协议无关 Trace ID
func TraceID(ctx context.Context) string {
	return traceInfo(ctx).traceID
}

// 读取请求信息
func traceInfo(ctx context.Context) requestInfo {
	if ctx == nil {
		return requestInfo{}
	}
	info, _ := ctx.Value(requestContextKey{}).(requestInfo)
	return info
}

// 校验 W3C Trace ID 文本
func validTraceID(traceID string) bool {
	if len(traceID) != 32 || traceID == strings.Repeat("0", 32) {
		return false
	}
	for _, value := range traceID {
		if value < '0' || (value > '9' && value < 'a') || value > 'f' {
			return false
		}
	}

	return true
}
