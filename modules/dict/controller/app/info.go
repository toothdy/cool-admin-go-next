package app

import (
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/dict/service"
)

// 字典信息接口
func AppDictInfoController(info *service.InfoService) gnctrl.Definition {
	return gnctrl.App().
		Options(gnctrl.RouterOptions{Description: "字典信息", TagName: "字典信息"}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/data",
				Summary:     "获得字典数据",
				Handler:     gnctrl.Handle(info.Data),
				Bind:        gnctrl.BindJSON,
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/types",
				Summary:     "获得所有字典类型",
				Handler:     gnctrl.Handle(info.Types),
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}
