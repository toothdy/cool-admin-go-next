package admin

import (
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/entity"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/service"
)

// 插件接口
func AdminPluginInfoController(info *service.InfoService) gnctrl.Definition {
	query := gnctrl.StaticQuery(gnctrl.QueryOp{
		Select: []gnctrl.SelectField{
			gnctrl.As(gnctrl.Field("id"), "id"),
			gnctrl.As(gnctrl.Field("name"), "name"),
			gnctrl.As(gnctrl.Field("keyName"), "keyName"),
			gnctrl.As(gnctrl.Field("hook"), "hook"),
			gnctrl.As(gnctrl.Field("version"), "version"),
			gnctrl.As(gnctrl.Field("status"), "status"),
			gnctrl.As(gnctrl.Field("readme"), "readme"),
			gnctrl.As(gnctrl.Field("author"), "author"),
			gnctrl.As(gnctrl.Field("logo"), "logo"),
			gnctrl.As(gnctrl.Field("description"), "description"),
			gnctrl.As(gnctrl.Field("manifest"), "pluginJson"),
			gnctrl.As(gnctrl.Field("config"), "config"),
			gnctrl.As(gnctrl.Field("createTime"), "createTime"),
			gnctrl.As(gnctrl.Field("updateTime"), "updateTime"),
		},
		KeyWordLikeFields: []gnctrl.ColumnRef{
			gnctrl.Field("name"),
			gnctrl.Field("description"),
			gnctrl.Field("keyName"),
			gnctrl.Field("author"),
		},
		FieldEq: []gnctrl.FieldEq{
			gnctrl.Eq(gnctrl.Field("status")),
			gnctrl.Eq(gnctrl.Field("hook")),
		},
		AddOrderBy: []gnctrl.Order{gnctrl.Desc(gnctrl.Field("id"))},
	})

	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "插件管理", TagName: "插件管理"}).
		Curd(gnctrl.CurdOption{
			API:         gnctrl.API(gnctrl.List, gnctrl.Page),
			Entity:      entity.Info{},
			Service:     info,
			PageQueryOp: query,
			ListQueryOp: query,
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/install",
				Summary:     "安装或升级插件",
				Handler:     gnctrl.Handle(info.InstallCompatible),
				Bind:        gnctrl.BindFile,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/info",
				Summary:     "插件详情",
				Handler:     gnctrl.Handle(info.Detail),
				Bind:        gnctrl.BindQuery,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/update",
				Summary:     "更新插件配置或状态",
				Handler:     gnctrl.Handle(info.Update),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/delete",
				Summary:     "卸载插件",
				Handler:     gnctrl.Handle(info.Delete),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}
