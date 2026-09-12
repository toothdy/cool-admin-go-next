package admin

import (
	"context"
	"net/http"

	"github.com/toothdy/cool-admin-go-next/cool-next/codegen"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/base"
)

// 批量创建 Go 文件的请求
type CodingCreateRequest struct {
	Codes []codegen.CodeFile `json:"codes" v:"required"`
}

// 实体无关的代码生成工具
type ToolHandler struct {
	scaffold *codegen.Scaffold
}

// 通用代码生成工具适配器
func NewToolHandler(config base.Config) (*ToolHandler, error) {
	scaffold, err := codegen.NewScaffold(config.Coding.Workspace)
	if err != nil {
		return nil, err
	}
	return &ToolHandler{scaffold: scaffold}, nil
}

// 开发环境代码生成接口
func AdminCodingController(handler *ToolHandler) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{
			Description:     "AI 编码",
			TagName:         "AI 编码",
			DevelopmentOnly: true,
		}).
		Route(
			gnctrl.Route{
				Method:      http.MethodGet,
				Path:        "/getModuleTree",
				Summary:     "获取模块目录结构",
				Handler:     gnctrl.Handle(handler.GetModuleTree),
				Transaction: gnctrl.NonTransactional(),
			},
			gnctrl.Route{
				Method:      http.MethodPost,
				Path:        "/createCode",
				Summary:     "创建代码",
				Handler:     gnctrl.Handle(handler.CreateCode),
				Bind:        gnctrl.BindJSON,
				Transaction: gnctrl.NonTransactional(),
			},
		).
		Build()
}

// 允许生成代码的模块名称
func (handler *ToolHandler) GetModuleTree(ctx context.Context) ([]string, error) {
	return handler.scaffold.GetModuleTree()
}

// 批量创建经过校验的 Go 文件
func (handler *ToolHandler) CreateCode(ctx context.Context, request *CodingCreateRequest) error {
	return handler.scaffold.CreateCode(request.Codes)
}
