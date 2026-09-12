package app

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/eps"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/service/upload"
)

// App 参数读取请求
type ParamQuery struct {
	Key string `json:"key" in:"query" v:"required"`
}

// 参数接口
type CommHandler struct {
	param *service.ParamService
}

// 通用接口适配器
func NewCommHandler(param *service.ParamService) *CommHandler {
	return &CommHandler{param: param}
}

// 通用接口
func AppCommController(handler *CommHandler, upload *upload.Service) gnctrl.Definition {
	return gnctrl.App().
		Options(gnctrl.RouterOptions{Description: "通用接口", TagName: "通用接口"}).
		Route(
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/param",
				Summary:     "参数配置",
				Handler:     gnctrl.Handle(handler.Param),
				Bind:        gnctrl.BindQuery,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/eps",
				Summary:     "实体信息与路径",
				Handler:     gnctrl.Handle(AppEPS),
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/upload",
				Summary:     "文件上传",
				Handler:     gnctrl.Handle(upload.AppUpload),
				Bind:        gnctrl.BindFile,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/uploadMode",
				Summary:     "文件上传模式",
				Handler:     gnctrl.Handle(upload.AppMode),
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}

// 配置允许公开的参数值
func (handler *CommHandler) Param(ctx context.Context, request *ParamQuery) (any, error) {
	return handler.param.AppDataByKey(ctx, request.Key)
}

// 已发布的 App EPS 视图（按模块分组的扁平 Controller 数组，兼容 cool-admin-vue 客户端契约）
func AppEPS(context.Context) (map[string][]eps.Controller, error) {
	return eps.AppView()
}
