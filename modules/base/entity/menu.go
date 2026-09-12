package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 后台目录、菜单和按钮
type Menu struct {
	g.Meta `orm:"table:base_sys_menu" description:"系统菜单"`
	gnentity.Base
	ParentID   *uint64 `json:"parentId" orm:"parentId" description:"父菜单ID"`
	ParentName *string `json:"parentName" description:"父菜单名称" cool:"transient"`
	Name       string  `json:"name" orm:"name" description:"菜单名称" cool:"size=255"`
	Router     *string `json:"router" orm:"router" description:"菜单地址" cool:"size=255"`
	Perms      *string `json:"perms" orm:"perms" description:"权限标识"`
	Type       int32   `json:"type" orm:"type" description:"类型 0-目录 1-菜单 2-按钮" cool:"default=0"`
	Icon       *string `json:"icon" orm:"icon" description:"图标" cool:"size=255"`
	OrderNum   int32   `json:"orderNum" orm:"orderNum" description:"排序" cool:"default=0"`
	ViewPath   *string `json:"viewPath" orm:"viewPath" description:"视图地址" cool:"size=255"`
	KeepAlive  bool    `json:"keepAlive" orm:"keepAlive" description:"路由缓存" cool:"default=true"`
	IsShow     bool    `json:"isShow" orm:"isShow" description:"是否显示" cool:"default=true"`
	SeedKey    *string `json:"seedKey" orm:"seedKey" description:"初始化键" cool:"size=255"`
}

// 菜单表补充索引
func MenuSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_base_sys_menu_parent_id", "parentId"),
		gnentity.UniqueIndexOf("uk_base_sys_menu_seed_key", "seedKey"),
	}}
}
