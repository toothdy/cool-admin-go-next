package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 后台系统用户
type User struct {
	g.Meta `orm:"table:base_sys_user" description:"系统用户"`
	gnentity.Base
	DepartmentID   *uint64   `json:"departmentId" orm:"departmentId" description:"部门ID"`
	DepartmentName *string   `json:"departmentName" description:"部门名称" cool:"transient"`
	UserID         *uint64   `json:"userId" orm:"userId" description:"创建者ID"`
	Name           *string   `json:"name" orm:"name" description:"姓名" cool:"size=255"`
	Username       string    `json:"username" orm:"username" description:"用户名" cool:"size=100"`
	Password       string    `json:"password" orm:"password" description:"密码" cool:"size=255"`
	PasswordV      int32     `json:"passwordV" orm:"passwordV" description:"密码版本" cool:"default=1"`
	NickName       *string   `json:"nickName" orm:"nickName" description:"昵称" cool:"size=255"`
	HeadImg        *string   `json:"headImg" orm:"headImg" description:"头像" cool:"size=255"`
	Phone          *string   `json:"phone" orm:"phone" description:"手机" cool:"size=20"`
	Email          *string   `json:"email" orm:"email" description:"邮箱" cool:"size=255"`
	Remark         *string   `json:"remark" orm:"remark" description:"备注" cool:"size=255"`
	Status         int32     `json:"status" orm:"status" description:"状态 0-禁用 1-启用" cool:"default=1"`
	SocketID       *string   `json:"socketId" orm:"socketId" description:"Socket ID" cool:"size=255"`
	RoleIDList     *[]uint64 `json:"roleIdList" description:"角色ID列表" cool:"transient"`
}

// 用户表补充索引
func UserSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_base_sys_user_department_id", "departmentId"),
		gnentity.IndexOf("idx_base_sys_user_user_id", "userId"),
		gnentity.UniqueIndexOf("uk_base_sys_user_username", "username"),
		gnentity.IndexOf("idx_base_sys_user_phone", "phone"),
	}}
}
