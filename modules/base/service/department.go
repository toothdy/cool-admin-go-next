package service

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type departmentRow struct {
	ID         uint64      `orm:"id"`
	CreateTime *gtime.Time `orm:"createTime"`
	UpdateTime *gtime.Time `orm:"updateTime"`
	Name       string      `orm:"name"`
	UserID     *uint64     `orm:"userId"`
	ParentID   *uint64     `orm:"parentId"`
	OrderNum   int32       `orm:"orderNum"`
}

type deptDeleteTree struct {
	RootID   uint64
	ParentID *uint64
	IDs      []uint64
}

type deptOrder struct {
	g.Meta   `orm:"do:true"`
	ParentID any `orm:"parentId"`
	OrderNum any `orm:"orderNum"`
}

// 部门树、用户归属及角色部门关系
type DepartmentService struct {
	*gnservice.Base[entity.Department, uint64]
	user           *gnservice.Base[entity.User, uint64]
	roleDepartment *gnservice.Base[entity.RoleDepartment, uint64]
	permission     *PermissionService
	boundary       *auth.Boundary
}

// 部门业务服务
func NewDepartment(
	department *gnservice.Base[entity.Department, uint64],
	user *gnservice.Base[entity.User, uint64],
	roleDepartment *gnservice.Base[entity.RoleDepartment, uint64],
	permission *PermissionService,
) *DepartmentService {

	return &DepartmentService{
		Base: department, user: user, roleDepartment: roleDepartment,
		permission: permission, boundary: permission.boundary,
	}
}

// 新增部门
func (s *DepartmentService) Add(ctx context.Context, input gnservice.AddInput[entity.Department]) (gnservice.AddResult[uint64], error) {
	values := input.Many()
	if !input.IsMany() {
		values = []*gnservice.Mutable[entity.Department]{input.One()}
	}
	parents := make([]uint64, 0, len(values))
	for _, value := range values {
		if parentID, exists := deptParentID(value); exists && parentID != nil {
			parents = append(parents, *parentID)
		}
	}
	if err := s.lockDepts(ctx, parents); err != nil {
		return gnservice.AddResult[uint64]{}, err
	}

	return s.Base.Add(ctx, input)
}

// 更新部门
func (s *DepartmentService) Update(ctx context.Context, input gnservice.UpdateInput[entity.Department, uint64]) error {
	var items []gnservice.UpdateItem[entity.Department, uint64]
	if input.IsMany() {
		items = input.Many()
	} else {
		items = []gnservice.UpdateItem[entity.Department, uint64]{input.One()}
	}
	ids := make([]uint64, 0, len(items)*2)
	for _, item := range items {
		ids = append(ids, item.ID())
		if parentID, exists := deptParentID(item.Mutable()); exists && parentID != nil {
			ids = append(ids, *parentID)
		}
	}
	if err := s.lockDepts(ctx, ids); err != nil {
		return err
	}

	return s.Base.Update(ctx, input)
}

// 按 Vue 顶层数组契约更新部门排序和父级
func (s *DepartmentService) Order(ctx context.Context, req dto.DepartmentOrderReq) error {
	if len(req) == 0 {
		return exception.Validate("部门排序参数无效")
	}
	ids := make([]uint64, 0, len(req)*2)
	for _, item := range req {
		ids = append(ids, item.ID)
		if item.ParentID != nil {
			ids = append(ids, *item.ParentID)
		}
	}
	if err := s.lockDepts(ctx, ids); err != nil {
		return err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}
	for _, item := range req {
		data := deptOrder{OrderNum: item.OrderNum}
		if item.ParentID == nil {
			data.ParentID = gdb.Raw("NULL")
		} else {
			data.ParentID = *item.ParentID
		}
		if _, err = model.Where("id", item.ID).Data(data).Update(); err != nil {
			return exception.WrapCore(err, "更新部门排序失败")
		}
	}

	return nil
}

// 当前用户可见的部门列表
func (s *DepartmentService) List(ctx context.Context) ([]dto.DepartmentListItem, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return nil, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	isAdmin, err := s.permission.IsAdmin(ctx, identity.RoleIDs())
	if err != nil {
		return nil, err
	}
	if !isAdmin {
		deptIDs, idErr := s.deptIDs(ctx, identity.RoleIDs())
		if idErr != nil {
			return nil, idErr
		}
		if len(deptIDs) == 0 {
			model = model.Where("userId", identity.UserID)
		} else {
			model = model.WhereIn("id", deptIDs).WhereOr("userId", identity.UserID)
		}
	}
	var rows []departmentRow
	if err = model.OrderAsc("orderNum").OrderAsc("id").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询部门列表失败")
	}
	parentIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		if row.ParentID != nil {
			parentIDs = append(parentIDs, *row.ParentID)
		}
	}
	parentNames, err := s.Names(ctx, parentIDs)
	if err != nil {
		return nil, err
	}
	items := make([]dto.DepartmentListItem, 0, len(rows))
	for _, row := range rows {
		var parentName *string
		if row.ParentID != nil {
			if name, exists := parentNames[*row.ParentID]; exists {
				parentName = &name
			}
		}
		items = append(items, dto.DepartmentListItem{ID: row.ID, CreateTime: row.CreateTime, UpdateTime: row.UpdateTime, Name: row.Name, UserID: row.UserID, ParentID: row.ParentID, OrderNum: row.OrderNum, ParentName: parentName})
	}
	return items, nil
}

// 删除部门树，并按请求迁移或删除归属用户
func (s *DepartmentService) Delete(ctx context.Context, req dto.DepartmentDeleteReq) error {
	ids := auth.NormalizeIDs(req.IDs)
	if len(ids) == 0 {
		return exception.Validate("部门 ID 不能为空")
	}
	if req.DeleteUser {
		adminRoles, err := s.permission.adminRoles(ctx)
		if err != nil {
			return err
		}
		if err = s.permission.lockRoles(ctx, adminRoles); err != nil {
			return err
		}
	}
	trees, err := s.lockTrees(ctx, ids)
	if err != nil {
		return err
	}
	allIDs := make([]uint64, 0)
	for _, tree := range trees {
		allIDs = append(allIDs, tree.IDs...)
	}
	allIDs = auth.NormalizeIDs(allIDs)
	userIDs, err := s.userIDs(ctx, allIDs)
	if err != nil {
		return err
	}
	if len(userIDs) > 0 {
		if err = s.permission.lockUsers(ctx, userIDs); err != nil {
			return err
		}
		if req.DeleteUser {
			if err = s.permission.keepAdmin(ctx, userIDs); err != nil {
				return err
			}
		}
		if err = s.permission.revoke(ctx, userIDs); err != nil {
			return err
		}
	}
	if req.DeleteUser && len(userIDs) > 0 {
		model, modelErr := s.user.Model(ctx)
		if modelErr != nil {
			return modelErr
		}
		if modelErr = s.permission.delRoles(ctx, userIDs); modelErr != nil {
			return modelErr
		}
		if _, modelErr = model.WhereIn("id", userIDs).Delete(); modelErr != nil {
			return exception.WrapCore(modelErr, "删除部门用户失败")
		}
	} else if len(userIDs) > 0 {
		model, modelErr := s.user.Model(ctx)
		if modelErr != nil {
			return modelErr
		}
		for _, tree := range trees {
			data, dataErr := s.userData(tree.ParentID)
			if dataErr != nil {
				return dataErr
			}
			if _, modelErr = model.WhereIn("departmentId", tree.IDs).Data(data).Update(); modelErr != nil {
				return exception.WrapCore(modelErr, "迁移部门用户失败")
			}
		}
	}
	model, err := s.roleDepartment.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.WhereIn("departmentId", allIDs).Delete(); err != nil {
		return exception.WrapCore(err, "清理部门角色关系失败")
	}
	deleteInput, err := gnservice.NewDeleteInput[entity.Department](s.Descriptor(), allIDs)
	if err != nil {
		return err
	}

	return s.Base.Delete(ctx, deleteInput)
}

// 按稳定 ID 顺序锁定并校验部门存在
func (s *DepartmentService) lockDepts(ctx context.Context, ids []uint64) error {
	return s.boundary.LockTable(ctx, s.Descriptor().Table(), ids, "锁定部门失败")
}

func (s *DepartmentService) lockTrees(ctx context.Context, roots []uint64) ([]deptDeleteTree, error) {
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []departmentRow
	if err = model.Fields("id", "parentId").OrderAsc("id").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询部门树失败")
	}
	allIDs := make([]uint64, len(rows))
	for i, row := range rows {
		allIDs[i] = row.ID
	}
	if err = s.lockDepts(ctx, allIDs); err != nil {
		return nil, err
	}
	model, err = s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	if err = model.Fields("id", "parentId").OrderAsc("id").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询锁定部门树失败")
	}
	existing := make(map[uint64]struct{}, len(rows))
	for _, row := range rows {
		existing[row.ID] = struct{}{}
	}
	for _, root := range roots {
		if _, exists := existing[root]; !exists {
			return nil, exception.Validate("部门不存在")
		}
	}
	return buildDeptTrees(rows, roots), nil
}

func (s *DepartmentService) userIDs(ctx context.Context, deptIDs []uint64) ([]uint64, error) {
	model, err := s.user.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields("id").WhereIn("departmentId", deptIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询部门用户失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return auth.NormalizeIDs(ids), nil
}

func (s *DepartmentService) deptIDs(ctx context.Context, roleIDs []uint64) ([]uint64, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	model, err := s.roleDepartment.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		DepartmentID uint64 `orm:"departmentId"`
	}
	if err = model.Fields("departmentId").WhereIn("roleId", roleIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色部门失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.DepartmentID
	}
	return auth.NormalizeIDs(ids), nil
}

// 当前用户按角色可见的部门 ID
func (s *DepartmentService) VisibleIDs(ctx context.Context, userID uint64, roleIDs []uint64) ([]uint64, error) {
	isAdmin, err := s.permission.IsAdmin(ctx, roleIDs)
	if err != nil || isAdmin {
		return nil, err
	}
	ids, err := s.deptIDs(ctx, roleIDs)
	if err != nil {
		return nil, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		model = model.Where("userId", userID)
	} else {
		model = model.WhereIn("id", ids).WhereOr("userId", userID)
	}
	var rows []struct {
		ID uint64 `orm:"id"`
	}
	if err = model.Fields("id").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询可见部门失败")
	}
	result := make([]uint64, len(rows))
	for i, row := range rows {
		result[i] = row.ID
	}

	return auth.NormalizeIDs(result), nil
}

// 查询单个部门名称
func (s *DepartmentService) Name(ctx context.Context, departmentID uint64) (string, error) {
	names, err := s.Names(ctx, []uint64{departmentID})
	if err != nil {
		return "", err
	}

	return names[departmentID], nil
}

// 批量查询部门名称
func (s *DepartmentService) Names(ctx context.Context, departmentIDs []uint64) (map[uint64]string, error) {
	ids := auth.NormalizeIDs(departmentIDs)
	result := make(map[uint64]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   uint64 `orm:"id"`
		Name string `orm:"name"`
	}
	if err = model.Fields("id", "name").WhereIn("id", ids).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "批量查询部门名称失败")
	}
	for _, row := range rows {
		result[row.ID] = row.Name
	}

	return result, nil
}

func (s *DepartmentService) userData(parentID *uint64) (any, error) {
	value := s.user.Descriptor().NewDO()
	if parentID == nil {
		if err := value.SetColumn("departmentId", nil); err != nil {
			return nil, err
		}
	} else if err := value.SetColumn("departmentId", *parentID); err != nil {
		return nil, err
	}
	return value.DBData(), nil
}

func deptParentID(value *gnservice.Mutable[entity.Department]) (*uint64, bool) {
	if !value.Has("parentId") {
		return nil, false
	}
	if value.IsNull("parentId") {
		return nil, true
	}
	current, _ := value.Get("parentId")
	parentID := current.(uint64)

	return &parentID, true
}

func buildDeptTrees(rows []departmentRow, roots []uint64) []deptDeleteTree {
	parents := make(map[uint64]*uint64, len(rows))
	children := make(map[uint64][]uint64)
	selected := make(map[uint64]struct{}, len(roots))
	for _, root := range roots {
		selected[root] = struct{}{}
	}
	for _, row := range rows {
		parents[row.ID] = row.ParentID
		if row.ParentID != nil {
			children[*row.ParentID] = append(children[*row.ParentID], row.ID)
		}
	}
	effective := make([]uint64, 0, len(roots))
	for _, root := range roots {
		parentID := parents[root]
		hasSelectedAncestor := false
		seen := map[uint64]struct{}{root: {}}
		for parentID != nil {
			if _, exists := selected[*parentID]; exists {
				hasSelectedAncestor = true
				break
			}
			if _, exists := seen[*parentID]; exists {
				break
			}
			seen[*parentID] = struct{}{}
			parentID = parents[*parentID]
		}
		if !hasSelectedAncestor {
			effective = append(effective, root)
		}
	}
	trees := make([]deptDeleteTree, 0, len(effective))
	for _, root := range effective {
		ids := []uint64{root}
		for i := 0; i < len(ids); i++ {
			ids = append(ids, children[ids[i]]...)
		}
		trees = append(trees, deptDeleteTree{RootID: root, ParentID: parents[root], IDs: auth.NormalizeIDs(ids)})
	}
	return trees
}
