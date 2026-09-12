package service

import (
	"context"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth/bcrypt"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type userRow struct {
	ID           uint64      `orm:"id"`
	CreateTime   *gtime.Time `orm:"createTime"`
	UpdateTime   *gtime.Time `orm:"updateTime"`
	DepartmentID *uint64     `orm:"departmentId"`
	UserID       *uint64     `orm:"userId"`
	Name         *string     `orm:"name"`
	Username     string      `orm:"username"`
	Password     string      `orm:"password"`
	PasswordV    int32       `orm:"passwordV"`
	NickName     *string     `orm:"nickName"`
	HeadImg      *string     `orm:"headImg"`
	Phone        *string     `orm:"phone"`
	Email        *string     `orm:"email"`
	Remark       *string     `orm:"remark"`
	Status       int32       `orm:"status"`
	SocketID     *string     `orm:"socketId"`
}

type userWrite struct {
	g.Meta       `orm:"do:true"`
	DepartmentID any `orm:"departmentId"`
	Name         any `orm:"name"`
	NickName     any `orm:"nickName"`
	HeadImg      any `orm:"headImg"`
	Phone        any `orm:"phone"`
	Email        any `orm:"email"`
	Password     any `orm:"password"`
	PasswordV    any `orm:"passwordV"`
}

// 用户分页响应
type UserPageResult struct {
	List       []dto.UserPageItem   `json:"list"`
	Pagination gnservice.Pagination `json:"pagination"`
}

// 后台用户业务服务
type UserService struct {
	*gnservice.Base[entity.User, uint64]
	permission *PermissionService
	department *DepartmentService
	password   *bcrypt.Verifier
}

// 用户业务服务
func NewUser(
	user *gnservice.Base[entity.User, uint64],
	permission *PermissionService,
	department *DepartmentService,
	password *bcrypt.Verifier,
) *UserService {

	return &UserService{Base: user, permission: permission, department: department, password: password}
}

// 新增用户及其角色关系
func (s *UserService) Add(ctx context.Context, input gnservice.AddInput[entity.User]) (gnservice.AddResult[uint64], error) {
	value := input.One()
	if value == nil {
		return gnservice.AddResult[uint64]{}, exception.Validate("用户新增只支持单条记录")
	}
	roles, hasRoles := roleIDs(value)
	if !hasRoles || len(roles) == 0 {
		return gnservice.AddResult[uint64]{}, exception.Validate("用户至少需要一个角色")
	}
	if err := s.hashPassword(value); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	deptIDs := userDeptIDs(value)
	if err := s.permission.lockRoles(ctx, roles); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	if err := s.department.lockDepts(ctx, deptIDs); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	result, err := s.Base.Add(ctx, input)
	if err != nil {
		return gnservice.AddResult[uint64]{}, err
	}
	if err = s.permission.setRoles(ctx, result.One(), roles); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}

	return result, nil
}

// 更新用户及其可选角色关系
func (s *UserService) Update(ctx context.Context, input gnservice.UpdateInput[entity.User, uint64]) error {
	item := input.One()
	if input.IsMany() || item.Mutable() == nil {
		return exception.Validate("用户更新只支持单条记录")
	}
	value := item.Mutable()
	roles, hasRoles := roleIDs(value)
	deptIDs := userDeptIDs(value)
	authChanged := value.Has("status") || hasRoles
	var before map[uint64][]uint64
	var err error
	if authChanged {
		before, err = s.permission.prepRoles(ctx, []uint64{item.ID()}, roles)
		if err != nil {
			return err
		}
	}
	if err = s.department.lockDepts(ctx, deptIDs); err != nil {
		return err
	}
	if err = s.permission.lockUsers(ctx, []uint64{item.ID()}); err != nil {
		return err
	}
	if authChanged {
		after, err := s.permission.roleSnap(ctx, []uint64{item.ID()})
		if err != nil {
			return err
		}
		if err = s.permission.checkSnap(before, after); err != nil {
			return err
		}
	}
	row, err := s.userByID(ctx, item.ID())
	if err != nil || row == nil {
		return err
	}
	oldRoles := before[item.ID()]
	newRoles := oldRoles
	if hasRoles {
		newRoles = roles
	}
	nextStatus := row.Status
	if status, exists := value.Get("status"); exists {
		nextStatus = status.(int32)
	}
	if authChanged {
		if err = s.permission.checkAdmin(ctx, item.ID(), row.Status, nextStatus, oldRoles, newRoles); err != nil {
			return err
		}
	}
	if err = s.updatePassword(value, row.PasswordV); err != nil {
		return err
	}
	shouldRevoke := value.Has("password") || value.Has("status") || hasRoles
	hasUpdate := false
	for _, field := range s.Descriptor().PersistentFields() {
		if value.Has(field.Name()) {
			hasUpdate = true
			break
		}
	}
	if hasUpdate {
		if err = s.Base.Update(ctx, input); err != nil {
			return err
		}
	}
	if hasRoles {
		if err = s.permission.setRoles(ctx, item.ID(), roles); err != nil {
			return err
		}
	}
	if shouldRevoke {
		return s.permission.revoke(ctx, []uint64{item.ID()})
	}

	return nil
}

// 删除用户及其角色关系
func (s *UserService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	ids := auth.NormalizeIDs(input.IDs())
	if len(ids) == 0 {
		return exception.Validate("用户 ID 不能为空")
	}
	before, err := s.permission.prepRoles(ctx, ids, nil)
	if err != nil {
		return err
	}
	if err = s.permission.lockUsers(ctx, ids); err != nil {
		return err
	}
	after, err := s.permission.roleSnap(ctx, ids)
	if err != nil {
		return err
	}
	if err = s.permission.checkSnap(before, after); err != nil {
		return err
	}
	if err = s.permission.keepAdmin(ctx, ids); err != nil {
		return err
	}
	if err = s.permission.delRoles(ctx, ids); err != nil {
		return err
	}
	if err = s.Base.Delete(ctx, input); err != nil {
		return err
	}

	return s.permission.revoke(ctx, ids)
}

// 用户详情及角色、部门虚拟字段
func (s *UserService) Info(ctx context.Context, userID uint64) (*dto.UserInfoResult, error) {
	record, err := s.Base.Info(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, exists := record.Get("id"); !exists {
		return nil, nil
	}
	var result dto.UserInfoResult
	if err = record.Scan(&result); err != nil {
		return nil, err
	}
	if err = s.enrichUserInfo(ctx, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// 按当前管理员的数据范围返回用户分页
func (s *UserService) Page(ctx context.Context, query gnservice.Query) (UserPageResult, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return UserPageResult{}, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return UserPageResult{}, err
	}
	model = model.WhereNot("username", "admin")
	isAdmin, err := s.permission.IsAdmin(ctx, identity.RoleIDs())
	if err != nil {
		return UserPageResult{}, err
	}
	if !isAdmin {
		deptIDs, rangeErr := s.department.deptIDs(ctx, identity.RoleIDs())
		if rangeErr != nil {
			return UserPageResult{}, rangeErr
		}
		if len(deptIDs) == 0 {
			model = model.Where("userId", identity.UserID)
		} else {
			scope := model.Builder().WhereIn("departmentId", deptIDs).WhereOr("userId", identity.UserID)
			model = model.Where(scope)
		}
	}
	var rows []userRow
	pagination, err := s.EntityRenderPage(ctx, model, query, &rows)
	if err != nil {
		return UserPageResult{}, err
	}
	items, err := s.pageItems(ctx, rows)
	if err != nil {
		return UserPageResult{}, err
	}

	return UserPageResult{List: items, Pagination: pagination}, nil
}

// 批量移动用户部门
func (s *UserService) Move(ctx context.Context, req *dto.UserMoveReq) error {
	if req == nil {
		return exception.Validate("移动用户参数无效")
	}
	userIDs := auth.NormalizeIDs(req.UserIDs)
	if req.DepartmentID == 0 || len(userIDs) == 0 {
		return exception.Validate("移动用户参数无效")
	}
	if err := s.department.lockDepts(ctx, []uint64{req.DepartmentID}); err != nil {
		return err
	}
	if err := s.permission.lockUsers(ctx, userIDs); err != nil {
		return err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.WhereIn("id", userIDs).Data(userWrite{DepartmentID: req.DepartmentID}).Update(); err != nil {
		return exception.WrapCore(err, "移动用户部门失败")
	}

	return s.permission.revoke(ctx, userIDs)
}

// 当前已认证管理员的个人资料
func (s *UserService) Person(ctx context.Context) (*dto.PersonResult, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.userByID(ctx, identity.UserID)
	if err != nil || row == nil {
		return nil, err
	}
	result := dto.PersonResult{
		ID: row.ID, CreateTime: row.CreateTime, UpdateTime: row.UpdateTime,
		DepartmentID: row.DepartmentID, UserID: row.UserID, Name: row.Name,
		Username: row.Username, PasswordV: row.PasswordV, NickName: row.NickName,
		HeadImg: row.HeadImg, Phone: row.Phone, Email: row.Email, Remark: row.Remark,
		Status: row.Status, SocketID: row.SocketID,
	}

	return &result, nil
}

// 更新当前用户允许修改的资料
func (s *UserService) PersonUpdate(ctx context.Context, req dto.PersonUpdateReq) error {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return err
	}
	if err = s.permission.lockUsers(ctx, []uint64{identity.UserID}); err != nil {
		return err
	}
	row, err := s.userByID(ctx, identity.UserID)
	if err != nil || row == nil {
		return err
	}
	data := userWrite{}
	hasUpdate := false
	if req.Name != nil {
		data.Name = *req.Name
		hasUpdate = true
	}
	if req.NickName != nil {
		data.NickName = *req.NickName
		hasUpdate = true
	}
	if req.HeadImg != nil {
		data.HeadImg = *req.HeadImg
		hasUpdate = true
	}
	if req.Phone != nil {
		data.Phone = *req.Phone
		hasUpdate = true
	}
	if req.Email != nil {
		data.Email = *req.Email
		hasUpdate = true
	}
	shouldRevoke := req.Password != nil && strings.TrimSpace(*req.Password) != ""
	if shouldRevoke {
		if req.OldPassword == nil || strings.TrimSpace(*req.OldPassword) == "" {
			return exception.Comm("原密码不能为空")
		}
		verified, verifyErr := s.password.Verify(*req.OldPassword, row.Password)
		if verifyErr != nil {
			return verifyErr
		}
		if !verified.Valid {
			return exception.Comm("原密码错误")
		}
		data.Password, err = s.password.Hash(*req.Password)
		if err != nil {
			return err
		}
		data.PasswordV = row.PasswordV + 1
		hasUpdate = true
	}
	if !hasUpdate {
		return nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.Where("id", identity.UserID).Data(data).Update(); err != nil {
		return exception.WrapCore(err, "更新个人资料失败")
	}
	if shouldRevoke {
		return s.permission.revoke(ctx, []uint64{identity.UserID})
	}

	return nil
}

func (s *UserService) hashPassword(value *gnservice.Mutable[entity.User]) error {
	password, exists := value.Get("password")
	if !exists || strings.TrimSpace(password.(string)) == "" {
		return exception.Validate("密码不能为空")
	}
	hash, err := s.password.Hash(password.(string))
	if err != nil {
		return err
	}

	return value.Set("password", hash)
}

func (s *UserService) updatePassword(value *gnservice.Mutable[entity.User], version int32) error {
	password, exists := value.Get("password")
	if !exists {
		return nil
	}
	plain := password.(string)
	if strings.TrimSpace(plain) == "" {
		return value.Unset("password")
	}
	hash, err := s.password.Hash(plain)
	if err != nil {
		return err
	}
	if err = value.Set("password", hash); err != nil {
		return err
	}

	return value.Set("passwordV", version+1)
}

func (s *UserService) userByID(ctx context.Context, userID uint64) (*userRow, error) {
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var find *userRow
	err = model.Where("id", userID).Scan(&find)
	if err != nil {
		return nil, exception.WrapCore(err, "查询用户失败")
	}

	return find, nil
}

func (s *UserService) enrichUserInfo(ctx context.Context, result *dto.UserInfoResult) error {
	roles, err := s.permission.RoleIDs(ctx, result.ID)
	if err != nil {
		return err
	}
	result.RoleIDList = roles
	if result.DepartmentID != nil {
		name, nameErr := s.department.Name(ctx, *result.DepartmentID)
		if nameErr != nil {
			return nameErr
		}
		result.DepartmentName = &name
	}

	return nil
}

func (s *UserService) pageItems(ctx context.Context, rows []userRow) ([]dto.UserPageItem, error) {
	userIDs := make([]uint64, 0, len(rows))
	deptIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.ID)
		if row.DepartmentID != nil {
			deptIDs = append(deptIDs, *row.DepartmentID)
		}
	}
	roles, err := s.permission.roles(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	deptNames, err := s.department.Names(ctx, deptIDs)
	if err != nil {
		return nil, err
	}
	items := make([]dto.UserPageItem, 0, len(rows))
	for _, row := range rows {
		var departmentName *string
		if row.DepartmentID != nil {
			if name, exists := deptNames[*row.DepartmentID]; exists {
				departmentName = &name
			}
		}
		role := roles[row.ID]
		items = append(items, dto.UserPageItem{
			ID: row.ID, CreateTime: row.CreateTime, UpdateTime: row.UpdateTime,
			DepartmentID: row.DepartmentID, Name: row.Name, Username: row.Username,
			NickName: row.NickName, HeadImg: row.HeadImg, Phone: row.Phone, Email: row.Email,
			Remark: row.Remark, Status: row.Status, DepartmentName: departmentName,
			RoleIDs: role.IDs, RoleName: strings.Join(role.Names, ","),
		})
	}

	return items, nil
}

func roleIDs(value *gnservice.Mutable[entity.User]) ([]uint64, bool) {
	if !value.Has("roleIdList") {
		return nil, false
	}
	if value.IsNull("roleIdList") {
		return []uint64{}, true
	}
	roles, _ := value.Get("roleIdList")

	return auth.NormalizeIDs(roles.([]uint64)), true
}

func userDeptIDs(value *gnservice.Mutable[entity.User]) []uint64 {
	if !value.Has("departmentId") || value.IsNull("departmentId") {
		return nil
	}
	valueID, _ := value.Get("departmentId")
	deptID := valueID.(uint64)
	if deptID == 0 {
		return nil
	}

	return []uint64{deptID}
}
