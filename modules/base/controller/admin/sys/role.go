package sys

import (
	"context"
	"net/http"
	"strconv"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

// 系统角色接口
func AdminSysRoleController(role *service.RoleService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "系统角色", TagName: "系统角色"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.API(gnctrl.Add, gnctrl.Delete, gnctrl.Update, gnctrl.Info, gnctrl.Page),
			Entity:  entity.Role{},
			Service: role,
			InsertParam: gnctrl.Insert(func(ctx context.Context, input *gnservice.Mutable[entity.Role]) error {
				identity, err := auth.Admin(ctx)
				if err != nil {
					return err
				}

				return input.Set("userId", strconv.FormatUint(identity.UserID, 10))
			}),
		}).
		Route(gnctrl.Route{
			Method:      http.MethodPost,
			Path:        "/list",
			Summary:     "列表查询",
			Handler:     gnctrl.Handle(role.List),
			Transaction: gnctrl.NonTransactional(),
		}).
		Build()
}
