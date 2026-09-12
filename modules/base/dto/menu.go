package dto

import "github.com/gogf/gf/v2/os/gtime"

// 静态解析菜单代码的请求
type MenuParseReq struct {
	Entity     string `json:"entity" v:"required"`
	Controller string `json:"controller"`
	Module     string `json:"module" v:"required"`
}

// 菜单导出的选中 ID 请求
type MenuExportReq struct {
	IDs []uint64 `json:"ids" v:"required"`
}

// 菜单列表的递归响应字段
type MenuListItem struct {
	ID         uint64         `json:"id"`
	CreateTime *gtime.Time    `json:"createTime"`
	UpdateTime *gtime.Time    `json:"updateTime"`
	ParentID   *uint64        `json:"parentId"`
	Name       string         `json:"name"`
	Router     *string        `json:"router"`
	Perms      *string        `json:"perms"`
	Type       int32          `json:"type"`
	Icon       *string        `json:"icon"`
	OrderNum   int32          `json:"orderNum"`
	ViewPath   *string        `json:"viewPath"`
	KeepAlive  bool           `json:"keepAlive"`
	IsShow     bool           `json:"isShow"`
	ParentName *string        `json:"parentName"`
	ChildMenus []MenuListItem `json:"childMenus,omitempty"`
}

// 菜单导入导出的稳定字段白名单，不含维护字段
type MenuTree struct {
	Name       string     `json:"name"`
	Router     *string    `json:"router"`
	Perms      *string    `json:"perms"`
	Type       int32      `json:"type"`
	Icon       *string    `json:"icon"`
	OrderNum   int32      `json:"orderNum"`
	ViewPath   *string    `json:"viewPath"`
	KeepAlive  bool       `json:"keepAlive"`
	IsShow     bool       `json:"isShow"`
	ChildMenus []MenuTree `json:"childMenus"`
}

// 当前用户的权限标识与菜单树
type PermissionMenuResult struct {
	Perms []string       `json:"perms"`
	Menus []MenuListItem `json:"menus"`
}
