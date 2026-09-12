package admin

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/space/entity"
	"github.com/toothdy/cool-admin-go-next/modules/space/service"
)

// 文件空间分类接口
func AdminSpaceTypeController(spaceType *service.TypeService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "文件空间分类", TagName: "文件空间分类"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Type{},
			Service: spaceType,
		}).
		Build()
}
