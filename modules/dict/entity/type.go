package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 字典类别
type Type struct {
	g.Meta `orm:"table:dict_type" description:"字典类别"`
	gnentity.Base
	Name string `json:"name" orm:"name" description:"名称" cool:"size=255"`
	Key  string `json:"key" orm:"key" description:"标识" cool:"size=255"`
}

// 字典类别表约束
func TypeSchema() gnentity.Schema {
	return gnentity.Schema{}
}
