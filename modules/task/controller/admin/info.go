package admin

import (
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/task/entity"
	"github.com/toothdy/cool-admin-go-next/modules/task/service"
)

// 任务接口
func AdminTaskInfoController(info *service.InfoService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "任务", TagName: "任务"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Info{},
			Service: info,
			PageQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
				FieldEq: []gnctrl.FieldEq{
					gnctrl.Eq(gnctrl.Field("status")),
					gnctrl.Eq(gnctrl.Field("type")),
				},
			}),
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/once",
				Summary:     "执行一次",
				Handler:     gnctrl.Handle(info.Once),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/stop",
				Summary: "停止",
				Handler: gnctrl.Handle(info.Stop),
				Bind:    gnctrl.BindJSON,
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/start",
				Summary: "开始",
				Handler: gnctrl.Handle(info.Start),
				Bind:    gnctrl.BindJSON,
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/log",
				Summary:     "日志",
				Handler:     gnctrl.Handle(info.Log),
				Bind:        gnctrl.BindQuery,
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}
