package main

import (
	"os"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	"github.com/gogf/gf/v2/os/gctx"
	"github.com/gogf/gf/v2/os/glog"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules"
)

func main() {
	ctx := gctx.GetInitCtx()
	logger := glog.Instance()
	if err := logger.SetPath("logs"); err != nil {
		logger.Error(ctx, "初始化系统日志失败", exception.LogText(err))
		os.Exit(1)
	}
	if err := app.Run(ctx, modules.Generated()); err != nil {
		logger.Error(ctx, exception.LogText(err))
		os.Exit(1)
	}
}
