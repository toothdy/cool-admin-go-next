package sys

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 适配部门删除和排序接口
type DepartmentHandler struct {
	department *service.DepartmentService
}

// 前端顶层数组请求契约
type DepartmentOrderBody struct {
	Items dto.DepartmentOrderReq `json:"-"`
}

// 系统部门接口
func AdminSysDepartmentController(department *service.DepartmentService, handler *DepartmentHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "系统部门", TagName: "系统部门"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.API(gnctrl.Add, gnctrl.Update),
			Entity:  entity.Department{},
			Service: department,
			InsertParam: gnctrl.Insert(func(ctx context.Context, input *gnservice.Mutable[entity.Department]) error {
				identity, err := auth.Admin(ctx)
				if err != nil {
					return err
				}

				return input.Set("userId", identity.UserID)
			}),
			HiddenFields:   []gnctrl.ColumnRef{gnctrl.Field("seedKey")},
			ReadonlyFields: []gnctrl.ColumnRef{gnctrl.Field("seedKey")},
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/list",
				Summary:     "列表",
				Handler:     gnctrl.Handle(department.List),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/delete",
				Summary: "删除",
				Handler: gnctrl.Handle(handler.Delete),
				Bind:    gnctrl.BindJSON,
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/order",
				Summary: "排序",
				Handler: gnctrl.Handle(handler.Order),
				Bind:    gnctrl.BindJSON,
			},
		).
		Build()
}

// 解码顶层部门排序数组
func (request *DepartmentOrderBody) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &request.Items)
}

// 部门接口适配器
func NewDepartmentHandler(department *service.DepartmentService) *DepartmentHandler {
	return &DepartmentHandler{department: department}
}

// 按用户处理策略删除部门树
func (handler *DepartmentHandler) Delete(ctx context.Context, request *dto.DepartmentDeleteReq) error {
	return handler.department.Delete(ctx, *request)
}

// 更新部门树顺序
func (handler *DepartmentHandler) Order(ctx context.Context, request *DepartmentOrderBody) error {
	return handler.department.Order(ctx, request.Items)
}
