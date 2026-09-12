package service

import (
	"context"
	"sort"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/seed"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type menuRow struct {
	ID         uint64      `orm:"id"`
	CreateTime *gtime.Time `orm:"createTime"`
	UpdateTime *gtime.Time `orm:"updateTime"`
	ParentID   *uint64     `orm:"parentId"`
	Name       string      `orm:"name"`
	Router     *string     `orm:"router"`
	Perms      *string     `orm:"perms"`
	Type       int32       `orm:"type"`
	Icon       *string     `orm:"icon"`
	OrderNum   int32       `orm:"orderNum"`
	ViewPath   *string     `orm:"viewPath"`
	KeepAlive  bool        `orm:"keepAlive"`
	IsShow     bool        `orm:"isShow"`
}

type menuExportRow struct {
	ID        uint64  `orm:"id"`
	ParentID  *uint64 `orm:"parentId"`
	Name      string  `orm:"name"`
	Router    *string `orm:"router"`
	Perms     *string `orm:"perms"`
	Type      int32   `orm:"type"`
	Icon      *string `orm:"icon"`
	OrderNum  int32   `orm:"orderNum"`
	ViewPath  *string `orm:"viewPath"`
	KeepAlive bool    `orm:"keepAlive"`
	IsShow    bool    `orm:"isShow"`
}

// 菜单树及角色菜单关系
type MenuService struct {
	*gnservice.Base[entity.Menu, uint64]
	runtime  *db.Runtime
	role     *gnservice.Base[entity.Role, uint64]
	roleMenu *gnservice.Base[entity.RoleMenu, uint64]
	userRole *gnservice.Base[entity.UserRole, uint64]
	boundary *auth.Boundary
}

// 菜单业务服务
func NewMenu(
	runtime *db.Runtime,
	menu *gnservice.Base[entity.Menu, uint64],
	role *gnservice.Base[entity.Role, uint64],
	roleMenu *gnservice.Base[entity.RoleMenu, uint64],
	userRole *gnservice.Base[entity.UserRole, uint64],
	sessions auth.Store,
) (*MenuService, error) {
	boundary, err := auth.NewBoundary(runtime, sessions)
	if err != nil {
		return nil, err
	}

	return &MenuService{Base: menu, runtime: runtime, role: role, roleMenu: roleMenu, userRole: userRole, boundary: boundary}, nil
}

// 新增菜单
func (s *MenuService) Add(ctx context.Context, input gnservice.AddInput[entity.Menu]) (gnservice.AddResult[uint64], error) {
	var result gnservice.AddResult[uint64]
	err := s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		values := input.Many()
		if !input.IsMany() {
			values = []*gnservice.Mutable[entity.Menu]{input.One()}
		}
		parents := make([]uint64, 0, len(values))
		for _, value := range values {
			if parentID, exists := menuParentID(value); exists && parentID != nil && *parentID != 0 {
				parents = append(parents, *parentID)
			}
		}
		if err := s.lockMenus(txCtx, parents); err != nil {
			return err
		}
		var err error
		result, err = s.Base.Add(txCtx, input)
		return err
	})
	return result, err
}

// 更新菜单并撤销受影响用户 Session
func (s *MenuService) Update(ctx context.Context, input gnservice.UpdateInput[entity.Menu, uint64]) error {
	return s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		var items []gnservice.UpdateItem[entity.Menu, uint64]
		if input.IsMany() {
			items = input.Many()
		} else {
			items = []gnservice.UpdateItem[entity.Menu, uint64]{input.One()}
		}
		targets := make([]uint64, 0, len(items))
		locks := make([]uint64, 0, len(items)*2)
		parents := make(map[uint64]*uint64, len(items))
		for _, item := range items {
			targets = append(targets, item.ID())
			locks = append(locks, item.ID())
			parentID, exists := menuParentID(item.Mutable())
			if exists {
				parents[item.ID()] = parentID
				if parentID != nil && *parentID != 0 {
					locks = append(locks, *parentID)
				}
			}
		}
		if len(parents) > 0 {
			model, err := s.Base.Model(txCtx)
			if err != nil {
				return err
			}
			var rows []struct {
				ID uint64 `orm:"id"`
			}
			if err = model.Fields("id").OrderAsc("id").Scan(&rows); err != nil {
				return exception.WrapCore(err, "查询菜单树失败")
			}
			for _, row := range rows {
				locks = append(locks, row.ID)
			}
		}
		if err := s.lockMenus(txCtx, locks); err != nil {
			return err
		}
		if err := s.checkParents(txCtx, parents); err != nil {
			return err
		}
		users, err := s.userIDs(txCtx, auth.NormalizeIDs(targets))
		if err != nil {
			return err
		}
		if err = s.boundary.LockUsersAndRevoke(txCtx, userTable, users, auth.AdminKind, "锁定授权用户失败"); err != nil {
			return err
		}
		return s.Base.Update(txCtx, input)
	})
}

// 递归删除菜单和角色关系
func (s *MenuService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	return s.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		ids, err := s.lockTree(txCtx, input.IDs())
		if err != nil {
			return err
		}
		users, err := s.userIDs(txCtx, ids)
		if err != nil {
			return err
		}
		if err = s.boundary.LockUsersAndRevoke(txCtx, userTable, users, auth.AdminKind, "锁定授权用户失败"); err != nil {
			return err
		}
		model, err := s.roleMenu.Model(txCtx)
		if err != nil {
			return err
		}
		if _, err = model.WhereIn("menuId", ids).Delete(); err != nil {
			return exception.WrapCore(err, "清理菜单角色关系失败")
		}
		deleteInput, err := gnservice.NewDeleteInput[entity.Menu](s.Descriptor(), ids)
		if err != nil {
			return err
		}
		return s.Base.Delete(txCtx, deleteInput)
	})
}

// 菜单详情
func (s *MenuService) Info(ctx context.Context, menuID uint64) (*dto.MenuListItem, error) {
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var row *menuRow
	if err = model.Where("id", menuID).Scan(&row); err != nil {
		return nil, exception.WrapCore(err, "查询菜单失败")
	}
	if row == nil {
		return nil, nil
	}
	item := menuItem(*row)
	item.ParentName, err = s.parentName(ctx, row.ParentID)
	return &item, err
}

// 当前用户可见的菜单树
func (s *MenuService) List(ctx context.Context) ([]dto.MenuListItem, error) {
	identity, err := auth.Admin(ctx)
	if err != nil {
		return nil, err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	if isAdmin, adminErr := s.isAdmin(ctx, identity.RoleIDs()); adminErr != nil {
		return nil, adminErr
	} else if !isAdmin {
		menuIDs, idErr := s.menuIDs(ctx, identity.RoleIDs())
		if idErr != nil {
			return nil, idErr
		}
		if len(menuIDs) == 0 {
			return []dto.MenuListItem{}, nil
		}
		model = model.WhereIn("id", menuIDs)
	}
	var rows []menuRow
	if err = model.OrderAsc("orderNum").Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询菜单列表失败")
	}
	return menuItems(rows), nil
}

// 导出选中的菜单树，不含维护字段
func (s *MenuService) Export(ctx context.Context, ids []uint64) ([]dto.MenuTree, error) {
	if len(ids) == 0 {
		return []dto.MenuTree{}, nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []menuExportRow
	if err = model.
		Fields("id", "parentId", "name", "router", "perms", "type", "icon", "orderNum", "viewPath", "keepAlive", "isShow").
		WhereIn("id", ids).
		Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询导出菜单失败")
	}
	sort.Slice(rows, func(left, right int) bool {
		if rows[left].OrderNum != rows[right].OrderNum {
			return rows[left].OrderNum < rows[right].OrderNum
		}

		return rows[left].ID < rows[right].ID
	})

	return menuTree(rows), nil
}

// 在调用方事务中插入菜单树，并用实际新 ID 重建父子关系
func (s *MenuService) Import(ctx context.Context, menus []dto.MenuTree) error {
	if _, err := s.Tx(ctx); err != nil {
		return err
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}

	return s.importTree(model, menus, nil)
}

func menuTree(rows []menuExportRow) []dto.MenuTree {
	children := make(map[uint64][]menuExportRow)
	roots := make([]menuExportRow, 0)
	for _, row := range rows {
		if row.ParentID == nil {
			roots = append(roots, row)
			continue
		}
		children[*row.ParentID] = append(children[*row.ParentID], row)
	}
	var walk func(menuExportRow, map[uint64]bool) dto.MenuTree
	walk = func(row menuExportRow, ancestors map[uint64]bool) dto.MenuTree {
		if ancestors[row.ID] {
			return treeNode(row, nil)
		}
		ancestors[row.ID] = true
		nested := children[row.ID]
		items := make([]dto.MenuTree, 0, len(nested))
		for _, child := range nested {
			items = append(items, walk(child, ancestors))
		}
		delete(ancestors, row.ID)

		return treeNode(row, items)
	}
	result := make([]dto.MenuTree, 0, len(roots))
	for _, root := range roots {
		result = append(result, walk(root, make(map[uint64]bool)))
	}

	return result
}

func treeNode(row menuExportRow, children []dto.MenuTree) dto.MenuTree {
	return dto.MenuTree{
		Name: row.Name, Router: row.Router, Perms: row.Perms, Type: row.Type, Icon: row.Icon,
		OrderNum: row.OrderNum, ViewPath: row.ViewPath, KeepAlive: row.KeepAlive, IsShow: row.IsShow,
		ChildMenus: children,
	}
}

func (s *MenuService) importTree(model *gdb.Model, menus []dto.MenuTree, parentID *uint64) error {
	for _, menu := range menus {
		fields := map[string]any{
			"name": menu.Name, "router": stringValue(menu.Router), "perms": stringValue(menu.Perms),
			"type": menu.Type, "icon": stringValue(menu.Icon), "orderNum": menu.OrderNum,
			"viewPath": stringValue(menu.ViewPath), "keepAlive": menu.KeepAlive, "isShow": menu.IsShow,
		}
		if parentID == nil {
			fields["parentId"] = nil
		} else {
			fields["parentId"] = *parentID
		}
		do, err := seed.NewDO(s.Descriptor(), fields, true)
		if err != nil {
			return exception.WrapCore(err, "构造导入节点失败")
		}
		insertedID, err := model.Data(do.DBData()).InsertAndGetId()
		if err != nil {
			return exception.WrapCore(err, "导入节点失败")
		}
		if insertedID <= 0 {
			return exception.Core("导入节点未返回有效 ID")
		}
		id := uint64(insertedID)
		if err = s.importTree(model, menu.ChildMenus, &id); err != nil {
			return err
		}
	}

	return nil
}

func stringValue(value *string) any {
	if value == nil {
		return nil
	}

	return *value
}

func (s *MenuService) lockMenus(ctx context.Context, ids []uint64) error {
	return s.boundary.LockTable(ctx, s.Descriptor().Table(), ids, "锁定授权菜单失败")
}

func (s *MenuService) lockTree(ctx context.Context, roots []uint64) ([]uint64, error) {
	roots = auth.NormalizeIDs(roots)
	if len(roots) == 0 {
		return nil, exception.Validate("菜单 ID 不能为空")
	}
	if err := s.lockMenus(ctx, roots); err != nil {
		return nil, err
	}
	seen := make(map[uint64]struct{}, len(roots))
	for _, root := range roots {
		seen[root] = struct{}{}
	}
	level := roots
	for len(level) > 0 {
		model, err := s.Base.Model(ctx)
		if err != nil {
			return nil, err
		}
		var children []struct {
			ID uint64 `orm:"id"`
		}
		if err = model.Fields("id").WhereIn("parentId", level).OrderAsc("id").Scan(&children); err != nil {
			return nil, exception.WrapCore(err, "查询菜单子节点失败")
		}
		candidates := make([]uint64, 0, len(children))
		for _, child := range children {
			if _, exists := seen[child.ID]; !exists {
				candidates = append(candidates, child.ID)
			}
		}
		candidates = auth.NormalizeIDs(candidates)
		if len(candidates) == 0 {
			break
		}
		if err = s.lockMenus(ctx, candidates); err != nil {
			return nil, err
		}
		model, err = s.Base.Model(ctx)
		if err != nil {
			return nil, err
		}
		var locked []menuRow
		if err = model.Fields("id", "parentId").WhereIn("id", candidates).OrderAsc("id").Scan(&locked); err != nil {
			return nil, exception.WrapCore(err, "查询锁定菜单子节点失败")
		}
		parents := make(map[uint64]struct{}, len(level))
		for _, parentID := range level {
			parents[parentID] = struct{}{}
		}
		level = level[:0]
		for _, child := range locked {
			if child.ParentID == nil {
				continue
			}
			if _, exists := parents[*child.ParentID]; !exists {
				continue
			}
			seen[child.ID] = struct{}{}
			level = append(level, child.ID)
		}
	}
	ids := make([]uint64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return auth.NormalizeIDs(ids), nil
}

func (s *MenuService) checkParents(ctx context.Context, changes map[uint64]*uint64) error {
	if len(changes) == 0 {
		return nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return err
	}
	var rows []menuRow
	if err = model.Fields("id", "parentId").OrderAsc("id").Scan(&rows); err != nil {
		return exception.WrapCore(err, "查询菜单树失败")
	}
	parents := make(map[uint64]*uint64, len(rows))
	for _, row := range rows {
		parents[row.ID] = row.ParentID
	}
	for id, parentID := range changes {
		if _, exists := parents[id]; !exists {
			return exception.Validate("菜单不存在")
		}
		if parentID != nil && *parentID != 0 {
			if _, exists := parents[*parentID]; !exists {
				return exception.Validate("菜单父级不存在")
			}
		}
		parents[id] = parentID
	}
	for id := range changes {
		seen := make(map[uint64]struct{})
		current := id
		for {
			if _, exists := seen[current]; exists {
				return exception.Validate("菜单父级不能形成循环")
			}
			seen[current] = struct{}{}
			parentID := parents[current]
			if parentID == nil || *parentID == 0 {
				break
			}
			current = *parentID
		}
	}
	return nil
}

func menuParentID(value *gnservice.Mutable[entity.Menu]) (*uint64, bool) {
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

func (s *MenuService) userIDs(ctx context.Context, menuIDs []uint64) ([]uint64, error) {
	if len(menuIDs) == 0 {
		return nil, nil
	}
	statement, err := gnservice.NativeSQL(`SELECT DISTINCT ur.userId FROM base_sys_role_menu rm INNER JOIN base_sys_user_role ur ON ur.roleId = rm.roleId WHERE rm.menuId IN (?)`, menuIDs)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		UserID uint64 `orm:"userId"`
	}
	if err = s.Base.NativeQuery(ctx, statement, &rows); err != nil {
		return nil, exception.WrapCore(err, "查询菜单用户失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.UserID
	}
	return auth.NormalizeIDs(ids), nil
}

func (s *MenuService) menuIDs(ctx context.Context, roleIDs []uint64) ([]uint64, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	model, err := s.roleMenu.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		MenuID uint64 `orm:"menuId"`
	}
	if err = model.Fields("menuId").WhereIn("roleId", roleIDs).Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询角色菜单失败")
	}
	ids := make([]uint64, len(rows))
	for i, row := range rows {
		ids[i] = row.MenuID
	}
	return auth.NormalizeIDs(ids), nil
}

func (s *MenuService) isAdmin(ctx context.Context, roleIDs []uint64) (bool, error) {
	model, err := s.role.Model(ctx)
	if err != nil {
		return false, err
	}
	count, err := model.WhereIn("id", roleIDs).Where("label", adminRoleLabel).Count()
	if err != nil {
		return false, exception.WrapCore(err, "查询管理员角色失败")
	}
	return count > 0, nil
}

func (s *MenuService) parentName(ctx context.Context, parentID *uint64) (*string, error) {
	if parentID == nil || *parentID == 0 {
		return nil, nil
	}
	model, err := s.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var row *struct {
		Name string `orm:"name"`
	}
	if err = model.Fields("name").Where("id", *parentID).Scan(&row); err != nil {
		return nil, exception.WrapCore(err, "查询菜单父节点失败")
	}
	if row == nil {
		return nil, nil
	}
	return &row.Name, nil
}

func menuItems(rows []menuRow) []dto.MenuListItem {
	names := make(map[uint64]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	items := make([]dto.MenuListItem, 0, len(rows))
	for _, row := range rows {
		item := menuItem(row)
		if row.ParentID != nil {
			if name, exists := names[*row.ParentID]; exists {
				item.ParentName = &name
			}
		}
		items = append(items, item)
	}

	return items
}

func menuItem(row menuRow) dto.MenuListItem {
	return dto.MenuListItem{ID: row.ID, CreateTime: row.CreateTime, UpdateTime: row.UpdateTime, ParentID: row.ParentID, Name: row.Name, Router: row.Router, Perms: row.Perms, Type: row.Type, Icon: row.Icon, OrderNum: row.OrderNum, ViewPath: row.ViewPath, KeepAlive: row.KeepAlive, IsShow: row.IsShow}
}
