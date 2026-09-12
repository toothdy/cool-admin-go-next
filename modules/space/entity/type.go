package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 文件空间分类
type Type struct {
	g.Meta `orm:"table:space_type" description:"文件空间分类"`
	gnentity.Base
	Name     string  `json:"name" orm:"name" description:"类别名称" cool:"size=255"`
	ParentID *uint64 `json:"parentId" orm:"parentId" description:"父分类ID"`
}

// 文件空间分类表约束
func TypeSchema() gnentity.Schema {
	return gnentity.Schema{}
}
