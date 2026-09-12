package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 后台部门节点
type Department struct {
	g.Meta `orm:"table:base_sys_department" description:"系统部门"`
	gnentity.Base
	Name       string  `json:"name" orm:"name" description:"部门名称" cool:"size=255"`
	UserID     *uint64 `json:"userId" orm:"userId" description:"创建者ID"`
	ParentID   *uint64 `json:"parentId" orm:"parentId" description:"上级部门ID"`
	ParentName *string `json:"parentName" description:"上级部门名称" cool:"transient"`
	OrderNum   int32   `json:"orderNum" orm:"orderNum" description:"排序" cool:"default=0"`
	SeedKey    *string `json:"seedKey" orm:"seedKey" description:"初始化键" cool:"size=255"`
}

// 部门表补充索引
func DepartmentSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_base_sys_department_user_id", "userId"),
		gnentity.IndexOf("idx_base_sys_department_parent_id", "parentId"),
		gnentity.UniqueIndexOf("uk_base_sys_department_seed_key", "seedKey"),
	}}
}
