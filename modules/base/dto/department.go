package dto

import "github.com/gogf/gf/v2/os/gtime"

// 删除部门及其用户处理策略的请求
type DepartmentDeleteReq struct {
	IDs        []uint64 `json:"ids" v:"required"`
	DeleteUser bool     `json:"deleteUser"`
}

// 部门树排序中的单个节点
type DepartmentOrderItem struct {
	ID       uint64  `json:"id" v:"required"`
	ParentID *uint64 `json:"parentId"`
	OrderNum int32   `json:"orderNum"`
}

// Vue 直接提交顶层数组的契约
type DepartmentOrderReq []DepartmentOrderItem

// 部门列表的稳定响应字段
type DepartmentListItem struct {
	ID         uint64      `json:"id"`
	CreateTime *gtime.Time `json:"createTime"`
	UpdateTime *gtime.Time `json:"updateTime"`
	Name       string      `json:"name"`
	UserID     *uint64     `json:"userId"`
	ParentID   *uint64     `json:"parentId"`
	OrderNum   int32       `json:"orderNum"`
	ParentName *string     `json:"parentName"`
}
