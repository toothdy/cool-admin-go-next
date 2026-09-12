package admin

import (
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/recycle/service"
)

// 回收站接口
func AdminRecycleDataController(data *service.DataService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "数据回收", TagName: "数据回收"}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/page",
				Summary:     "分页",
				Handler:     gnctrl.Handle(data.Page),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/info",
				Summary:     "详情",
				Handler:     gnctrl.Handle(data.Info),
				Bind:        gnctrl.BindQuery,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/restore",
				Summary:     "恢复",
				Handler:     gnctrl.Handle(data.Restore),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}
