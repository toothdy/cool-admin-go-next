package sys

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/codegen"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/base"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 系统菜单接口
func AdminSysMenuController(menu *service.MenuService, tool *MenuToolHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "系统菜单", TagName: "系统菜单"}).
		Curd(gnctrl.CurdOption{
			API:            gnctrl.API(gnctrl.Add, gnctrl.Delete, gnctrl.Update, gnctrl.Info, gnctrl.Page),
			Entity:         entity.Menu{},
			Service:        menu,
			HiddenFields:   []gnctrl.ColumnRef{gnctrl.Field("seedKey")},
			ReadonlyFields: []gnctrl.ColumnRef{gnctrl.Field("seedKey")},
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/list",
				Summary:     "列表查询",
				Handler:     gnctrl.Handle(menu.List),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:          http.MethodPost,
				Path:            "/parse",
				Summary:         "解析",
				DevelopmentOnly: true,
				Handler:         gnctrl.Handle(tool.ParseMenu),
				Bind:            gnctrl.BindJSON,
				Transaction:     gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:          http.MethodPost,
				Path:            "/create",
				Summary:         "创建代码",
				DevelopmentOnly: true,
				Handler:         gnctrl.Handle(tool.CreateMenuCode),
				Bind:            gnctrl.BindJSON,
				Transaction:     gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/export",
				Summary:     "导出",
				Handler:     gnctrl.Handle(tool.ExportMenu),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/import",
				Summary: "导入",
				Handler: gnctrl.Handle(tool.ImportMenu),
				Bind:    gnctrl.BindJSON,
			},
		).
		Build()
}

// 菜单树导入请求
type MenuImportRequest struct {
	Menus []dto.MenuTree `json:"menus" v:"required"`
}

// 菜单代码生成与树导入导出工具
type MenuToolHandler struct {
	scaffold *codegen.Scaffold
	menu     *service.MenuService
}

// 菜单工具接口适配器
func NewMenuToolHandler(config base.Config, menu *service.MenuService) (*MenuToolHandler, error) {
	scaffold, err := codegen.NewScaffold(config.Coding.Workspace)
	if err != nil {
		return nil, err
	}

	return &MenuToolHandler{scaffold: scaffold, menu: menu}, nil
}

// 静态解析菜单代码元数据
func (handler *MenuToolHandler) ParseMenu(ctx context.Context, request *dto.MenuParseReq) (codegen.MenuParseResult, error) {
	return handler.scaffold.ParseMenu(request.Entity, request.Controller, request.Module)
}

// 创建菜单对应的 Go 文件
func (handler *MenuToolHandler) CreateMenuCode(ctx context.Context, request *codegen.MenuCreateInput) error {
	return handler.scaffold.CreateMenuCode(*request)
}

// 导出选中的菜单树，不含维护字段
func (handler *MenuToolHandler) ExportMenu(ctx context.Context, request *dto.MenuExportReq) ([]dto.MenuTree, error) {
	if len(request.IDs) == 0 {
		return []dto.MenuTree{}, nil
	}

	return handler.menu.Export(ctx, request.IDs)
}

// 在调用方事务中插入菜单树，并用实际新 ID 重建父子关系
func (handler *MenuToolHandler) ImportMenu(ctx context.Context, request *MenuImportRequest) error {
	return handler.menu.Import(ctx, request.Menus)
}
