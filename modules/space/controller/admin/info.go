package admin

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/space/entity"
	"github.com/toothdy/cool-admin-go-next/modules/space/service"
)

// 文件空间信息接口
func AdminSpaceInfoController(info *service.InfoService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "文件空间信息", TagName: "文件空间信息"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Info{},
			Service: info,
			PageQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
				FieldEq: []gnctrl.FieldEq{
					gnctrl.Eq(gnctrl.Field("type")),
					gnctrl.Eq(gnctrl.Field("classifyId")),
				},
			}),
		}).
		Build()
}
