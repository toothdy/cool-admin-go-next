package dto

import "github.com/gogf/gf/v2/os/gtime"

// 批量移动用户部门的请求
type UserMoveReq struct {
	DepartmentID uint64   `json:"departmentId" v:"required"`
	UserIDs      []uint64 `json:"userIds" v:"required"`
}

// 当前用户可修改的个人资料白名单
type PersonUpdateReq struct {
	Name        *string `json:"name"`
	NickName    *string `json:"nickName"`
	HeadImg     *string `json:"headImg"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
	Password    *string `json:"password"`
	OldPassword *string `json:"oldPassword"`
}

// 当前管理员个人信息
type PersonResult struct {
	ID           uint64      `json:"id"`
	CreateTime   *gtime.Time `json:"createTime"`
	UpdateTime   *gtime.Time `json:"updateTime"`
	DepartmentID *uint64     `json:"departmentId"`
	UserID       *uint64     `json:"userId"`
	Name         *string     `json:"name"`
	Username     string      `json:"username"`
	PasswordV    int32       `json:"passwordV"`
	NickName     *string     `json:"nickName"`
	HeadImg      *string     `json:"headImg"`
	Phone        *string     `json:"phone"`
	Email        *string     `json:"email"`
	Remark       *string     `json:"remark"`
	Status       int32       `json:"status"`
	SocketID     *string     `json:"socketId"`
}

// 用户分页列表的稳定响应字段
type UserPageItem struct {
	ID             uint64      `json:"id"`
	CreateTime     *gtime.Time `json:"createTime"`
	UpdateTime     *gtime.Time `json:"updateTime"`
	DepartmentID   *uint64     `json:"departmentId"`
	Name           *string     `json:"name"`
	Username       string      `json:"username"`
	NickName       *string     `json:"nickName"`
	HeadImg        *string     `json:"headImg"`
	Phone          *string     `json:"phone"`
	Email          *string     `json:"email"`
	Remark         *string     `json:"remark"`
	Status         int32       `json:"status"`
	DepartmentName *string     `json:"departmentName"`
	RoleIDs        []uint64    `json:"roleIds"`
	RoleName       string      `json:"roleName"`
}

// 用户详情表单的稳定响应字段
type UserInfoResult struct {
	ID             uint64      `json:"id"`
	CreateTime     *gtime.Time `json:"createTime"`
	UpdateTime     *gtime.Time `json:"updateTime"`
	DepartmentID   *uint64     `json:"departmentId"`
	UserID         *uint64     `json:"userId"`
	Name           *string     `json:"name"`
	Username       string      `json:"username"`
	PasswordV      int32       `json:"passwordV"`
	NickName       *string     `json:"nickName"`
	HeadImg        *string     `json:"headImg"`
	Phone          *string     `json:"phone"`
	Email          *string     `json:"email"`
	Remark         *string     `json:"remark"`
	Status         int32       `json:"status"`
	SocketID       *string     `json:"socketId"`
	DepartmentName *string     `json:"departmentName"`
	RoleIDList     []uint64    `json:"roleIdList"`
}
