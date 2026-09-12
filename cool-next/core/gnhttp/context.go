package gnhttp

import (
	"context"
	"mime"
	"strconv"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/net/gtrace"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/gnrecycle"
)

// HTTP 认证内核
type Authenticator interface {
	AuthenticateHTTP(context.Context, string, string, string, string, bool) (context.Context, error)
}

// 创建协议无关请求上下文中间件。权限标识由最终路径推导，推导失败即启动失败
func NewContextMiddleware(authenticator Authenticator, requestPath string, ignoreToken bool) (ghttp.HandlerFunc, error) {
	if authenticator == nil {
		return nil, exception.Core("HTTP Authenticator 不能为空")
	}
	if _, err := auth.HTTPResource("GET", requestPath); err != nil {
		return nil, err
	}
	permission, err := auth.DerivePermission(requestPath, ignoreToken)
	if err != nil {
		return nil, err
	}
	rule := auth.Rule{Permission: permission, IgnoreToken: ignoreToken}

	return func(request *ghttp.Request) {
		if err := authenticateRequest(request, authenticator, requestPath, rule); err != nil {
			request.SetError(err)
			request.Middleware.Next()
			return
		}
		request.Middleware.Next()
	}, nil
}

// 认证单个 HTTP 请求
func authenticateRequest(request *ghttp.Request, authenticator Authenticator, requestPath string, rule auth.Rule) error {
	if request == nil || request.Request == nil {
		return exception.Core("HTTP 请求不能为空")
	}
	ctx, err := withRequestTrace(request.Context())
	if err != nil {
		return err
	}
	request.SetCtx(ctx)
	verified, err := authenticator.AuthenticateHTTP(
		ctx,
		request.Header.Get("Authorization"),
		request.Method,
		requestPath,
		rule.Permission,
		rule.IgnoreToken,
	)
	if err != nil {
		return err
	}
	audit, err := gnrecycle.NewAudit(requestAuditInput(request, verified))
	if err != nil {
		return exception.WrapCore(err, "构造 HTTP 删除审计失败")
	}
	request.SetCtx(gnrecycle.WithAudit(verified, audit))

	return nil
}

// 构造 HTTP 删除审计输入
func requestAuditInput(request *ghttp.Request, ctx context.Context) gnrecycle.AuditInput {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	params := request.GetQueryMap()
	if err != nil || mediaType != "multipart/form-data" {
		params = request.GetMap()
	}
	input := gnrecycle.AuditInput{Params: params}
	if request.URL != nil {
		input.Source = request.URL.RequestURI()
	}
	if identity, err := auth.Admin(ctx); err == nil {
		input.OperatorType = string(auth.AdminKind)
		input.OperatorID = strconv.FormatUint(identity.UserID, 10)
		return input
	}
	if identity, err := auth.App(ctx); err == nil {
		input.OperatorType = string(auth.AppKind)
		input.OperatorID = strconv.FormatUint(identity.ID, 10)
	}

	return input
}

// 复用或建立 HTTP Trace ID
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
			return ctx, exception.WrapCore(err, "写入 HTTP Trace ID 失败")
		}
	}

	return app.WithTraceID(ctx, traceID)
}
