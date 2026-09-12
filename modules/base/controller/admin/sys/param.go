package sys

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 按参数键读取富文本的查询请求
type ParamHTMLQuery struct {
	Key string `json:"key" in:"query" v:"required"`
}

// 适配后台参数富文本接口
type ParamHTMLHandler struct {
	param *service.ParamService
}

// 系统参数接口
func AdminSysParamController(param *service.ParamService, handler *ParamHTMLHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "参数配置", TagName: "参数配置"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.API(gnctrl.Add, gnctrl.Delete, gnctrl.Update, gnctrl.Info, gnctrl.Page),
			Entity:  entity.Param{},
			Service: param,
		}).
		Route(gnctrl.Route{
			Method:      http.MethodGet,
			Path:        "/html",
			Summary:     "获得网页内容的参数值",
			Handler:     gnctrl.Handle(handler.HTML),
			Bind:        gnctrl.BindQuery,
			Transaction: gnctrl.NonTransactional(),
		}).
		Build()
}

// 参数富文本接口适配器
func NewParamHTMLHandler(param *service.ParamService) *ParamHTMLHandler {
	return &ParamHTMLHandler{param: param}
}

// 按参数键返回原始 HTML
func (handler *ParamHTMLHandler) HTML(ctx context.Context, request *ParamHTMLQuery) (gnctrl.HTMLResponse, error) {
	return handler.param.HTMLByKey(ctx, request.Key)
}
