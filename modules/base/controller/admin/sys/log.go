package sys

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 操作日志保留天数请求
type LogKeepReq struct {
	Value int `json:"value" v:"required|min:1"`
}

// 适配操作日志自定义接口
type LogHandler struct {
	log *service.LogService
}

// 系统操作日志接口
func AdminSysLogController(log *service.LogService, handler *LogHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "系统操作日志", TagName: "系统操作日志"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.API(gnctrl.Page),
			Entity:  entity.Log{},
			Service: log,
		}).
		Route(
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/clear",
				Summary: "清理",
				Handler: gnctrl.Handle(handler.Clear),
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/setKeep",
				Summary: "日志保存时间",
				Handler: gnctrl.Handle(handler.SetKeep),
				Bind:    gnctrl.BindJSON,
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/getKeep",
				Summary:     "获得日志保存时间",
				Handler:     gnctrl.Handle(handler.GetKeep),
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}

// 操作日志接口适配器
func NewLogHandler(log *service.LogService) *LogHandler {
	return &LogHandler{log: log}
}

// 清空全部操作日志
func (handler *LogHandler) Clear(ctx context.Context) error {
	_, err := handler.log.Clear(ctx, true)

	return err
}

// 设置操作日志保留天数
func (handler *LogHandler) SetKeep(ctx context.Context, request *LogKeepReq) error {
	return handler.log.SetKeep(ctx, request.Value)
}

// 返回操作日志保留天数
func (handler *LogHandler) GetKeep(ctx context.Context) (int, error) {
	return handler.log.GetKeep(ctx)
}
