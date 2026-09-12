package service

import (
	"context"
	"strconv"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type roleRow struct {
	ID               uint64      `orm:"id"`
	CreateTime       *gtime.Time `orm:"createTime"`
	UpdateTime       *gtime.Time `orm:"updateTime"`
	UserID           string      `orm:"userId"`
	Name             string      `orm:"name"`
	Label            *string     `orm:"label"`
	Remark           *string     `orm:"remark"`
	Relevance        bool        `orm:"relevance"`
	MenuIDList       []uint64    `orm:"menuIdList"`
	DepartmentIDList []uint64    `orm:"departmentIdList"`
}

type roleRelationWrite struct {
	g.Meta       `orm:"do:true"`
	RoleID       any `orm:"roleId"`
	MenuID       any `orm:"menuId"`
	DepartmentID any `orm:"departmentId"`
}

// 角色分页响应
type RolePageResult struct {
	List       []dto.RoleInfoResult `json:"list"`
	Pagination gnservice.Pagination `json:"pagination"`
}

// 角色及其权威菜单、部门关系
type RoleService struct {
	*gnservice.Base[entity.Role, uint64]
	runtime        *db.Runtime
	userRole       *gnservice.Base[entity.UserRole, uint64]
	roleMenu       *gnservice.Base[entity.RoleMenu, uint64]
	roleDepartment *gnservice.Base[entity.RoleDepartment, uint64]
	boundary       *auth.Boundary
}

// 角色业务服务
func NewRole(
	runtime *db.Runtime,
	role *gnservice.Base[entity.Role, uint64],
	userRole *gnservice.Base[entity.UserRole, uint64],
	roleMenu *gnservice.Base[entity.RoleMenu, uint64],
	roleDepartment *gnservice.Base[entity.RoleDepartment, uint64],
	sessions auth.Store,
) (*RoleService, error) {
	boundary, err := auth.NewBoundary(runtime, sessions)
	if err != nil {
		return nil, err
	}

	return &RoleService{
		Base: role, runtime: runtime, userRole: userRole, roleMenu: roleMenu,
		roleDepartment: roleDepartment, boundary: boundary,
	}, nil
}

// 按菜单、部门顺序锁定并校验角色权限资源存在
func (s *RoleService) lockPerms(ctx context.Context, menuIDs, deptIDs []uint64) error {
	if err := s.boundary.LockTable(ctx, menuTable, menuIDs, "锁定授权菜单失败"); err != nil {
		return err
	}

	return s.boundary.LockTable(ctx, departmentTable, deptIDs, "锁定授权部门失败")
}

// 新建角色并同步菜单、部门权限关系
func (s *RoleService) Add(
	ctx context.Context,
	input gnservice.AddInput[entity.Role],
) (gnservice.AddResult[uint64], error) {
	value := input.One()
	if value == nil {
		return gnservice.AddResult[uint64]{}, exception.Validate("角色新增只支持单条记录")
	}
	perms := dto.RolePermissionInput{}
	if submitted := rolePerms(value); submitted != nil {
		perms = *submitted
	}
	var result gnservice.AddResult[uint64]
	err := s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		if err := setRolePerms(value, perms); err != nil {
			return err
		}
		if err := s.lockPerms(txCtx, perms.MenuIDList, perms.DepartmentIDList); err != nil {
			return err
		}
		var addErr error
		result, addErr = s.Base.Add(txCtx, input)
		if addErr != nil {
			return addErr
		}

		return s.replacePerms(txCtx, result.One(), perms)
	})

	return result, err
}

// 更新角色及可选权限关系，并撤销受影响用户 Session
func (s *RoleService) Update(
	ctx context.Context,
	input gnservice.UpdateInput[entity.Role, uint64],
) error {
	var items []gnservice.UpdateItem[entity.Role, uint64]
	if input.IsMany() {
		items = input.Many()
	} else {
		items = []gnservice.UpdateItem[entity.Role, uint64]{input.One()}
	}
	if len(items) != 1 {
		return exception.Validate("角色更新只支持单条记录")
	}
	item := items[0]
	perms := rolePerms(item.Mutable())

	return s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		var (
			err      error
			keepMenu bool
			keepDept bool
		)
		if perms != nil {
			resolved := *perms
			if !item.Mutable().Has("menuIdList") {
				keepMenu = true
				resolved.MenuIDList, err = roleRelationIDs(txCtx, s.roleMenu, "menuId", item.ID())
				if err != nil {
					return err
				}
			}
			if !item.Mutable().Has("departmentIdList") {
				keepDept = true
				resolved.DepartmentIDList, err = roleRelationIDs(txCtx, s.roleDepartment, "departmentId", item.ID())
				if err != nil {
					return err
				}
			}
			perms = &resolved
			if err = setRolePerms(item.Mutable(), resolved); err != nil {
				return err
			}
			if err = s.lockPerms(txCtx, resolved.MenuIDList, resolved.DepartmentIDList); err != nil {
				return err
			}
		}
		if err := s.boundary.LockTable(txCtx, s.Descriptor().Table(), []uint64{item.ID()}, "锁定授权角色失败"); err != nil {
			return err
		}
		row, err := s.roleByID(txCtx, item.ID())
		if err != nil || row == nil {
			return err
		}
		if perms != nil {
			if keepMenu {
				current, relationErr := roleRelationIDs(txCtx, s.roleMenu, "menuId", item.ID())
				if relationErr != nil {
					return relationErr
				}
				if relationErr = auth.ValidateSnapshot(perms.MenuIDList, current, "角色菜单权限已变更，请重试"); relationErr != nil {
					return relationErr
				}
			}
			if keepDept {
				current, relationErr := roleRelationIDs(txCtx, s.roleDepartment, "departmentId", item.ID())
				if relationErr != nil {
					return relationErr
				}
				if relationErr = auth.ValidateSnapshot(perms.DepartmentIDList, current, "角色部门权限已变更，请重试"); relationErr != nil {
					return relationErr
				}
			}
		}
		if err = checkAdminRole(*row, item.Mutable()); err != nil {
			return err
		}
		userIDs, err := s.userIDs(txCtx, []uint64{item.ID()})
		if err != nil {
			return err
		}
		if err = s.boundary.LockUsersAndRevoke(txCtx, userTable, userIDs, auth.AdminKind, "锁定授权用户失败"); err != nil {
			return err
		}
		if err = s.Base.Update(txCtx, input); err != nil {
			return err
		}
		if perms != nil {
			return s.replacePerms(txCtx, item.ID(), *perms)
		}

		return nil
	})
}

// 删除角色及全部关系，并保护平台管理员角色
func (s *RoleService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	return s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		roleIDs := auth.NormalizeIDs(input.IDs())
		menuIDs, deptIDs, err := s.permIDs(txCtx, roleIDs)
		if err != nil {
			return err
		}
		if err = s.lockPerms(txCtx, menuIDs, deptIDs); err != nil {
			return err
		}
		if err := s.boundary.LockTable(txCtx, s.Descriptor().Table(), roleIDs, "锁定授权角色失败"); err != nil {
			return err
		}
		lockedMenu, lockedDept, err := s.permIDs(txCtx, roleIDs)
		if err != nil {
			return err
		}
		if err = auth.ValidateSnapshot(menuIDs, lockedMenu, "角色权限已变更，请重试"); err != nil {
			return err
		}
		if err = auth.ValidateSnapshot(deptIDs, lockedDept, "角色权限已变更，请重试"); err != nil {
			return err
		}
		if err := s.rejectAdminRole(txCtx, roleIDs); err != nil {
			return err
		}
		userIDs, err := s.userIDs(txCtx, roleIDs)
		if err != nil {
			return err
		}
		if err = s.boundary.LockUsersAndRevoke(txCtx, userTable, userIDs, auth.AdminKind, "锁定授权用户失败"); err != nil {
			return err
		}
		for _, relation := range []interface {
			Model(context.Context) (*gdb.Model, error)
		}{s.userRole, s.roleMenu, s.roleDepartment} {
			model, modelErr := relation.Model(txCtx)
			if modelErr != nil {
				return modelErr
			}
			if _, modelErr = model.WhereIn("roleId", roleIDs).Delete(); modelErr != nil {
				return exception.WrapCore(modelErr, "清理角色关系失败")
			}
		}

		return s.Base.Delete(txCtx, input)
	})
}

// 角色详情及权威权限关系
func (s *RoleService) Info(ctx context.Context, roleID uint64) (*dto.RoleInfoResult, error) {
	row, err := s.roleByID(ctx, roleID)
	if err != nil || row == nil {
		return nil, err
	}
	row.MenuIDList, err = roleRelationIDs(ctx, s.roleMenu, "menuId", roleID)
	if err != nil {
		return nil, err
	}
	row.DepartmentIDList, err = roleRelationIDs(ctx, s.roleDepartment, "departmentId", roleID)
	if err != nil {
		return nil, err
	}
	result := dto.RoleInfoResult{
		ID: row.ID, CreateTime: row.CreateTime, UpdateTime: row.UpdateTime,
		UserID: row.UserID, Name: row.Name, Label: row.Label, Remark: row.Remark,
		Relevance: row.Relevance, MenuIDList: row.MenuIDList,
		DepartmentIDList: row.DepartmentIDList,
	}

	return &result, nil
}

// 当前管理员可见的非平台管理员角色
func (s *RoleService) List(ctx context.Context) ([]dto.RoleInfoResult, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return nil, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	model, err = s.visibleRoles(ctx, model, identity)
	if err != nil {
		return nil, err
	}
	var rows []roleRow
	if err = model.OrderAsc("id").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色列表失败")
	}
	result := make([]dto.RoleInfoResult, 0, len(rows))
	for _, row := range rows {
		info, infoErr := s.Info(ctx, row.ID)
		if infoErr != nil {
			return nil, infoErr
		}
		result = append(result, *info)
	}

	return result, nil
}

// 当前管理员可见的角色分页
func (s *RoleService) Page(ctx context.Context, query gnservice.Query) (RolePageResult, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return RolePageResult{}, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return RolePageResult{}, err
	}
	model, err = s.visibleRoles(ctx, model, identity)
	if err != nil {
		return RolePageResult{}, err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	pagination, err := s.EntityRenderPage(ctx, model, query, &rows)
	if err != nil {
		return RolePageResult{}, err
	}
	items := make([]dto.RoleInfoResult, 0, len(rows))
	for _, row := range rows {
		info, infoErr := s.Info(ctx, row.ID)
		if infoErr != nil {
			return RolePageResult{}, infoErr
		}
		items = append(items, *info)
	}

	return RolePageResult{
		List:       items,
		Pagination: pagination,
	}, nil
}

func (s *RoleService) visibleRoles(
	ctx context.Context,
	model *gdb.Model,
	identity auth.AdminIdentity,
) (*gdb.Model, error) {
	model = model.Where("label IS NULL OR label <> ?", adminRoleLabel)
	isAdmin, err := s.isAdmin(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !isAdmin {
		model = model.Where("userId = ? OR id IN (?)", strconv.FormatUint(identity.UserID, 10), identity.RoleIDs())
	}

	return model, nil
}

func (s *RoleService) replacePerms(
	ctx context.Context,
	roleID uint64,
	perms dto.RolePermissionInput,
) error {
	if err := replaceRoleRelation(ctx, s.roleMenu, "menuId", roleID, perms.MenuIDList); err != nil {
		return err
	}

	return replaceRoleRelation(ctx, s.roleDepartment, "departmentId", roleID, perms.DepartmentIDList)
}

func (s *RoleService) permIDs(
	ctx context.Context,
	roleIDs []uint64,
) ([]uint64, []uint64, error) {
	menuIDs, err := roleRelIDs(ctx, s.roleMenu, "menuId", roleIDs)
	if err != nil {
		return nil, nil, err
	}
	deptIDs, err := roleRelIDs(ctx, s.roleDepartment, "departmentId", roleIDs)
	if err != nil {
		return nil, nil, err
	}

	return menuIDs, deptIDs, nil
}

func (s *RoleService) roleByID(ctx context.Context, roleID uint64) (*roleRow, error) {
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var row *roleRow
	if err = model.Where("id", roleID).Scan(&row); err != nil {
		return nil, exception.WrapCore(err, "查询角色失败")
	}

	return row, nil
}

func (s *RoleService) userIDs(ctx context.Context, roleIDs []uint64) ([]uint64, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	model, err := s.userRole.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		UserID uint64 `orm:"userId"`
	}
	if err = model.Fields("userId").WhereIn("roleId", roleIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色用户失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.UserID
	}

	return auth.NormalizeIDs(ids), nil
}

func (s *RoleService) rejectAdminRole(ctx context.Context, roleIDs []uint64) error {
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}
	count, err := model.WhereIn("id", roleIDs).Where("label", adminRoleLabel).Count()
	if err != nil {
		return exception.WrapCore(err, "查询管理员角色失败")
	}
	if count > 0 {
		return exception.Comm("不能删除平台管理员角色")
	}

	return nil
}

func (s *RoleService) isAdmin(ctx context.Context, identity auth.AdminIdentity) (bool, error) {
	if len(identity.RoleIDs()) == 0 {
		return false, nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return false, err
	}
	count, err := model.WhereIn("id", identity.RoleIDs()).Where("label", adminRoleLabel).Count()
	if err != nil {
		return false, exception.WrapCore(err, "查询管理员角色失败")
	}

	return count > 0, nil
}

func checkAdminRole(row roleRow, mutable *gnservice.Mutable[entity.Role]) error {
	if row.Label == nil || *row.Label != adminRoleLabel {
		return nil
	}
	if name, exists := mutable.Get("name"); exists && name != row.Name {
		return exception.Comm("不能修改平台管理员角色名称")
	}
	if label, exists := mutable.Get("label"); exists {
		value, ok := label.(*string)
		if !ok || value == nil || *value != adminRoleLabel {
			return exception.Comm("不能修改平台管理员角色标识")
		}
	}

	return nil
}

func setRolePerms(
	mutable *gnservice.Mutable[entity.Role],
	perms dto.RolePermissionInput,
) error {
	if err := mutable.Set("menuIdList", auth.NormalizeIDs(perms.MenuIDList)); err != nil {
		return err
	}

	return mutable.Set("departmentIdList", auth.NormalizeIDs(perms.DepartmentIDList))
}

func rolePerms(mutable *gnservice.Mutable[entity.Role]) *dto.RolePermissionInput {
	perms := new(dto.RolePermissionInput)
	has := false
	if value, ok := mutable.Get("menuIdList"); ok {
		perms.MenuIDList = value.([]uint64)
		has = true
	}
	if value, ok := mutable.Get("departmentIdList"); ok {
		perms.DepartmentIDList = value.([]uint64)
		has = true
	}
	if !has {
		return nil
	}

	return perms
}

func replaceRoleRelation[E any](
	ctx context.Context,
	base *gnservice.Base[E, uint64],
	column string,
	roleID uint64,
	ids []uint64,
) error {
	model, err := base.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.Where("roleId", roleID).Delete(); err != nil {
		return exception.WrapCore(err, "删除旧角色关系失败")
	}
	ids = auth.NormalizeIDs(ids)
	rows := make([]roleRelationWrite, len(ids))
	for i, id := range ids {
		rows[i].RoleID = roleID
		switch column {
		case "menuId":
			rows[i].MenuID = id
		case "departmentId":
			rows[i].DepartmentID = id
		default:
			return exception.Core("角色关系字段无效")
		}
	}
	if len(rows) > 0 {
		if _, err = model.Data(rows).Insert(); err != nil {
			return exception.WrapCore(err, "写入角色关系失败")
		}
	}

	return nil
}

func roleRelationIDs[E any](
	ctx context.Context,
	base *gnservice.Base[E, uint64],
	column string,
	roleID uint64,
) ([]uint64, error) {
	model, err := base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields(column+" AS id").Where("roleId", roleID).OrderAsc(column).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色关系失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	return ids, nil
}

func roleRelIDs[E any](
	ctx context.Context,
	base *gnservice.Base[E, uint64],
	column string,
	roleIDs []uint64,
) ([]uint64, error) {
	roleIDs = auth.NormalizeIDs(roleIDs)
	if len(roleIDs) == 0 {
		return nil, nil
	}
	model, err := base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields(column+" AS id").WhereIn("roleId", roleIDs).OrderAsc(column).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色关系失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	return auth.NormalizeIDs(ids), nil
}
