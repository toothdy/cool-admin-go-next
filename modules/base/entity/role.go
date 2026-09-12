package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 后台用户角色
type Role struct {
	g.Meta `orm:"table:base_sys_role" description:"系统角色"`
	gnentity.Base
	UserID           string   `json:"userId" orm:"userId" description:"创建者ID" cool:"size=255"`
	Name             string   `json:"name" orm:"name" description:"名称" cool:"size=255"`
	Label            *string  `json:"label" orm:"label" description:"角色标签" cool:"size=50"`
	Remark           *string  `json:"remark" orm:"remark" description:"备注" cool:"size=255"`
	Relevance        bool     `json:"relevance" orm:"relevance" description:"数据权限是否关联上下级" cool:"default=false"`
	MenuIDList       []uint64 `json:"menuIdList" orm:"menuIdList" description:"菜单权限" cool:"json=true"`
	DepartmentIDList []uint64 `json:"departmentIdList" orm:"departmentIdList" description:"部门权限" cool:"json=true"`
}

// 角色表补充索引
func RoleSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.UniqueIndexOf("uk_base_sys_role_name", "name"),
		gnentity.UniqueIndexOf("uk_base_sys_role_label", "label"),
	}}
}
