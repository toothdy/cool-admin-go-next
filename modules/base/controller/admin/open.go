package admin

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/eps"
	"github.com/toothdy/cool-admin-go-next/cool-next/risk"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 按参数键读取富文本的查询请求
type HTMLQuery struct {
	Key string `json:"key" in:"query" v:"required"`
}

// 公开接口
type OpenHandler struct {
	login   *service.LoginService
	captcha *service.CaptchaService
	param   *service.ParamService
	risk    *risk.Service
}

// 公开接口适配器
func NewOpenHandler(
	login *service.LoginService,
	captcha *service.CaptchaService,
	param *service.ParamService,
	riskService *risk.Service,
) *OpenHandler {
	return &OpenHandler{login: login, captcha: captcha, param: param, risk: riskService}
}

// 公开接口
func AdminOpenController(handler *OpenHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "开放接口", TagName: "开放接口"}).
		Route(
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/eps",
				Summary:     "实体信息与路径",
				Handler:     gnctrl.Handle(AdminEPS),
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/html",
				Summary:     "参数值",
				Handler:     gnctrl.Handle(handler.HTML),
				Bind:        gnctrl.BindQuery,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/login",
				Summary:     "登录",
				Handler:     gnctrl.Handle(handler.Login),
				Bind:        gnctrl.BindJSON,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/captcha",
				Summary:     "验证码",
				Handler:     gnctrl.Handle(handler.Captcha),
				Bind:        gnctrl.BindQuery,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/refreshToken",
				Summary:     "刷新token",
				Handler:     gnctrl.Handle(handler.Refresh),
				Bind:        gnctrl.BindJSON,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}

// 按参数键返回公开白名单内的清洗 HTML
func (handler *OpenHandler) HTML(ctx context.Context, request *HTMLQuery) (gnctrl.HTMLResponse, error) {
	return handler.param.PublicHTMLByKey(ctx, request.Key)
}

// 后台登录
func (handler *OpenHandler) Login(ctx context.Context, request *dto.LoginReq) (dto.TokenResult, error) {
	clientIP, err := handler.risk.ResolveClientIP(ctx)
	if err != nil {
		return dto.TokenResult{}, err
	}

	return handler.login.Login(ctx, clientIP, *request)
}

// 图形验证码
func (handler *OpenHandler) Captcha(ctx context.Context, request *dto.CaptchaQuery) (dto.CaptchaResult, error) {
	clientIP, err := handler.risk.ResolveClientIP(ctx)
	if err != nil {
		return dto.CaptchaResult{}, err
	}

	return handler.captcha.Generate(ctx, clientIP, *request)
}

// 原子刷新后台令牌
func (handler *OpenHandler) Refresh(ctx context.Context, request *dto.RefreshReq) (dto.TokenResult, error) {
	return handler.login.Refresh(ctx, *request)
}

func AdminEPS(context.Context) (map[string][]eps.Controller, error) {
	return eps.AdminView()
}
