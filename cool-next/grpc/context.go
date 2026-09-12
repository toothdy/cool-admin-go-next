package grpc

import (
	"context"
	"strings"

	"github.com/gogf/gf/v2/net/gtrace"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// gRPC 认证内核
type Authenticator interface {
	AuthenticateGRPC(context.Context, string, string, string, bool) (context.Context, error)
}

// gRPC 方法鉴权规则解析器
type RuleResolver func(string) (auth.Rule, error)

// 构造 Unary 鉴权拦截器
func UnaryContext(authenticator Authenticator, resolver RuleResolver) (googlegrpc.UnaryServerInterceptor, error) {
	if authenticator == nil {
		return nil, exception.Core("gRPC Authenticator 不能为空")
	}
	if resolver == nil {
		return nil, exception.Core("gRPC RuleResolver 不能为空")
	}

	return func(
		ctx context.Context,
		request any,
		info *googlegrpc.UnaryServerInfo,
		handler googlegrpc.UnaryHandler,
	) (any, error) {
		if info == nil || handler == nil {
			return nil, exception.Core("gRPC Unary 调用信息无效")
		}
		verified, err := authenticate(ctx, authenticator, resolver, info.FullMethod)
		if err != nil {
			return nil, Error(err)
		}

		return handler(verified, request)
	}, nil
}

// 构造 Stream 鉴权拦截器
func StreamContext(authenticator Authenticator, resolver RuleResolver) (googlegrpc.StreamServerInterceptor, error) {
	if authenticator == nil {
		return nil, exception.Core("gRPC Authenticator 不能为空")
	}
	if resolver == nil {
		return nil, exception.Core("gRPC RuleResolver 不能为空")
	}

	return func(
		server any,
		stream googlegrpc.ServerStream,
		info *googlegrpc.StreamServerInfo,
		handler googlegrpc.StreamHandler,
	) error {
		if stream == nil || info == nil || handler == nil {
			return exception.Core("gRPC Stream 调用信息无效")
		}
		verified, err := authenticate(stream.Context(), authenticator, resolver, info.FullMethod)
		if err != nil {
			return Error(err)
		}

		return handler(server, &contextServerStream{ServerStream: stream, ctx: verified})
	}, nil
}

type contextServerStream struct {
	googlegrpc.ServerStream
	ctx context.Context
}

// 鉴权后请求上下文
func (stream *contextServerStream) Context() context.Context { return stream.ctx }

// 建立并认证 gRPC 请求上下文
func authenticate(
	ctx context.Context,
	authenticator Authenticator,
	resolver RuleResolver,
	fullMethod string,
) (context.Context, error) {
	if ctx == nil {
		return nil, exception.Core("gRPC 请求上下文不能为空")
	}
	rule, err := resolver(fullMethod)
	if err != nil {
		return ctx, err
	}
	if rule.IgnoreToken && strings.TrimSpace(rule.Permission) != "" {
		return ctx, exception.Core("gRPC ignoreToken 与权限不能同时配置")
	}
	ctx, err = withRequestTrace(ctx)
	if err != nil {
		return ctx, err
	}

	return authenticator.AuthenticateGRPC(
		ctx,
		authMetadata(ctx),
		fullMethod,
		rule.Permission,
		rule.IgnoreToken,
	)
}

// 复用或建立 gRPC Trace ID
func withRequestTrace(ctx context.Context) (context.Context, error) {
	traceID := gtrace.GetTraceID(ctx)
	if traceID == "" {
		var err error
		traceID, err = app.NewTraceID()
		if err != nil {
			return ctx, err
		}
		ctx, err = gtrace.WithTraceID(ctx, traceID)
		if err != nil {
			return ctx, exception.WrapCore(err, "写入 gRPC Trace ID 失败")
		}
	}

	return app.WithTraceID(ctx, traceID)
}

func authMetadata(ctx context.Context) string {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return ""
	}

	return values[0]
}
