package admin

import (
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/dict/entity"
	"github.com/toothdy/cool-admin-go-next/modules/dict/service"
)

// 字典信息接口
func AdminDictInfoController(info *service.InfoService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "字典信息", TagName: "字典信息"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Info{},
			Service: info,
			ListQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
				KeyWordLikeFields: []gnctrl.ColumnRef{gnctrl.Field("name")},
				FieldEq:           []gnctrl.FieldEq{gnctrl.Eq(gnctrl.Field("typeId"))},
				AddOrderBy:        []gnctrl.Order{gnctrl.Asc(gnctrl.Field("createTime"))},
			}),
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/data",
				Summary:     "获得字典数据",
				Handler:     gnctrl.Handle(info.Data),
				Bind:        gnctrl.BindJSON,
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
