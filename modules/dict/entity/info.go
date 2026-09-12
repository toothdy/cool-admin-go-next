package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 字典信息
type Info struct {
	g.Meta `orm:"table:dict_info" description:"字典信息"`
	gnentity.Base
	TypeID   uint64  `json:"typeId" orm:"typeId" description:"类型ID"`
	Name     string  `json:"name" orm:"name" description:"名称" cool:"size=255"`
	Value    *string `json:"value" orm:"value" description:"值" cool:"size=255"`
	OrderNum int32   `json:"orderNum" orm:"orderNum" description:"排序" cool:"default=0"`
	Remark   *string `json:"remark" orm:"remark" description:"备注" cool:"size=255"`
	ParentID *uint64 `json:"parentId" orm:"parentId" description:"父ID"`
}

// 字典信息表约束
func InfoSchema() gnentity.Schema {
	return gnentity.Schema{}
}
