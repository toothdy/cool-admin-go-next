package admin

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/dict/entity"
	"github.com/toothdy/cool-admin-go-next/modules/dict/service"
)

// 字典类型接口
func AdminDictTypeController(dictType *service.TypeService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "字典类型", TagName: "字典类型"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Type{},
			Service: dictType,
			ListQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
				KeyWordLikeFields: []gnctrl.ColumnRef{gnctrl.Field("name")},
			}),
		}).
		Build()
}
