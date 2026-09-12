package admin

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
	"github.com/toothdy/cool-admin-go-next/modules/plugin/service/upload"
)

// 适配后台通用业务接口
type CommHandler struct {
	user       *service.UserService
	permission *service.PermissionService
}

// 后台通用接口适配器
func NewCommHandler(
	user *service.UserService,
	permission *service.PermissionService,
) *CommHandler {
	return &CommHandler{user: user, permission: permission}
}

// 通用接口
func AdminCommController(handler *CommHandler, login *service.LoginService, upload *upload.Service) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{
			Description: "通用接口",
			TagName:     "通用接口",
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/person",
				Summary:     "详情",
				Handler:     gnctrl.Handle(handler.Person),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:  http.MethodPost,
				Path:    "/personUpdate",
				Summary: "修改",
				Handler: gnctrl.Handle(handler.PersonUpdate),
				Bind:    gnctrl.BindJSON,
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/permmenu",
				Summary:     "权限菜单",
				Handler:     gnctrl.Handle(handler.PermissionMenu),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/upload",
				Summary:     "文件上传",
				Handler:     gnctrl.Handle(upload.AdminUpload),
				Bind:        gnctrl.BindFile,
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/uploadMode",
				Summary:     "文件上传模式",
				Handler:     gnctrl.Handle(upload.AdminMode),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/logout",
				Summary:     "退出",
				Handler:     gnctrl.Handle(login.Logout),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/program",
				Summary:     "编程",
				Handler:     gnctrl.Handle(Program),
				Tags:        []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}

// 当前管理员个人信息
func (handler *CommHandler) Person(ctx context.Context) (*dto.PersonResult, error) {
	return handler.user.Person(ctx)
}

// 当前管理员个人信息
func (handler *CommHandler) PersonUpdate(ctx context.Context, request *dto.PersonUpdateReq) error {
	return handler.user.PersonUpdate(ctx, *request)
}

// 当前管理员的权限与菜单树
func (handler *CommHandler) PermissionMenu(ctx context.Context) (dto.PermissionMenuResult, error) {
	return handler.permission.PermissionMenu(ctx)
}

// 当前后端实现语言
func Program(context.Context) (string, error) {
	return "Go", nil
}
