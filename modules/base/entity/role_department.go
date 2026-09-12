package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 角色与部门关系
type RoleDepartment struct {
	g.Meta `orm:"table:base_sys_role_department" description:"角色部门关系"`
	gnentity.Base
	RoleID       uint64 `json:"roleId" orm:"roleId" description:"角色ID"`
	DepartmentID uint64 `json:"departmentId" orm:"departmentId" description:"部门ID"`
}

// 角色部门关系表补充索引
func RoleDepartmentSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.UniqueIndexOf("uk_base_sys_role_department_role_department", "roleId", "departmentId"),
		gnentity.IndexOf("idx_base_sys_role_department_department_id", "departmentId"),
	}}
}
