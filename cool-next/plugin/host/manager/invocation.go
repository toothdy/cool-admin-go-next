package manager

import (
	"context"
	"encoding/json"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

type invocationKey struct{}

type generationKey struct{}

// 传给插件的已认证身份摘要
type IdentitySummary struct {
	Type string
	ID   uint64
}

// 一次插件调用的不可变上下文快照
type InvocationContext struct {
	ID       int64
	Source   string
	Identity IdentitySummary
	Chain    []string
}

// 当前插件 generation 的只读元数据
type GenerationMetadata struct {
	Key     string
	Version string
	Config  json.RawMessage
}

// 返回调用上下文副本
func InvocationFromContext(ctx context.Context) (InvocationContext, bool) {
	if ctx == nil {
		return InvocationContext{}, false
	}
	invocation, exists := ctx.Value(invocationKey{}).(InvocationContext)
	if !exists {
		return InvocationContext{}, false
	}
	invocation.Chain = append([]string(nil), invocation.Chain...)
	return invocation, true
}

// 返回当前插件 generation 元数据副本
func GenerationFromContext(ctx context.Context) (GenerationMetadata, bool) {
	if ctx == nil {
		return GenerationMetadata{}, false
	}
	metadata, exists := ctx.Value(generationKey{}).(GenerationMetadata)
	if !exists {
		return GenerationMetadata{}, false
	}
	metadata.Config = append(json.RawMessage(nil), metadata.Config...)

	return metadata, true
}

// 写入静态宿主调用来源和身份摘要
func WithInvocationSource(ctx context.Context, source string, identity IdentitySummary) context.Context {
	invocation, _ := InvocationFromContext(ctx)
	invocation.Source = source
	invocation.Identity = identity
	return withInvocation(ctx, invocation)
}

// 写入调用上下文副本
func withInvocation(ctx context.Context, invocation InvocationContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	invocation.Chain = append([]string(nil), invocation.Chain...)
	return context.WithValue(ctx, invocationKey{}, invocation)
}

// 写入 Manager 解析出的 generation 元数据副本
func withGeneration(ctx context.Context, metadata GenerationMetadata) context.Context {
	metadata.Config = append(json.RawMessage(nil), metadata.Config...)
	return withGenerationSnapshot(ctx, metadata)
}

func withGenerationSnapshot(ctx context.Context, metadata GenerationMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, generationKey{}, metadata)
}

// 将插件加入调用链并检查环路和深度
func enterPlugin(ctx context.Context, key string, maxDepth int) (context.Context, error) {
	invocation, _ := InvocationFromContext(ctx)
	for _, current := range invocation.Chain {
		if current == key {
			return nil, protocol.NewError(protocol.ErrorCallCycle, "插件调用形成环路")
		}
	}
	if maxDepth <= 0 || len(invocation.Chain) >= maxDepth {
		return nil, protocol.NewError(protocol.ErrorCallDepthExceeded, "插件调用超过最大深度")
	}
	invocation.Chain = append(invocation.Chain, key)
	return withInvocation(ctx, invocation), nil
}

// 为调用添加默认期限
func withCallTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}
