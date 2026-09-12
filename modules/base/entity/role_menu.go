package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 角色与菜单关系
type RoleMenu struct {
	g.Meta `orm:"table:base_sys_role_menu" description:"角色菜单关系"`
	gnentity.Base
	RoleID uint64 `json:"roleId" orm:"roleId" description:"角色ID"`
	MenuID uint64 `json:"menuId" orm:"menuId" description:"菜单ID"`
}

// 角色菜单关系表补充索引
func RoleMenuSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.UniqueIndexOf("uk_base_sys_role_menu_role_menu", "roleId", "menuId"),
		gnentity.IndexOf("idx_base_sys_role_menu_menu_id", "menuId"),
	}}
}
