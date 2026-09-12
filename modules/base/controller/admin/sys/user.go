package sys

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 系统用户接口
func AdminSysUserController(user *service.UserService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "系统用户", TagName: "系统用户"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.User{},
			Service: user,
			PageQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
				KeyWordLikeFields: []gnctrl.ColumnRef{
					gnctrl.Field("name"),
					gnctrl.Field("username"),
				},
				FieldEq: []gnctrl.FieldEq{
					gnctrl.Eq(gnctrl.Field("status")),
					gnctrl.EqFrom(gnctrl.Field("departmentId"), "departmentIds"),
				},
			}),
			InsertParam: gnctrl.Insert(func(ctx context.Context, input *gnservice.Mutable[entity.User]) error {
				identity, err := auth.Admin(ctx)
				if err != nil {
					return err
				}

				return input.Set("userId", identity.UserID)
			}),
			HiddenFields:   []gnctrl.ColumnRef{gnctrl.Field("password")},
			ReadonlyFields: []gnctrl.ColumnRef{gnctrl.Field("passwordV"), gnctrl.Field("socketId")},
		}).
		Route(gnctrl.Route{
			Method:  http.MethodPost,
			Path:    "/move",
			Summary: "移动部门",
			Handler: gnctrl.Handle(user.Move),
			Bind:    gnctrl.BindJSON,
		}).
		Build()
}
