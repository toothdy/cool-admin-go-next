package service

import (
	"context"

	"github.com/gogf/gf/v2/frame/g"
)

// 演示任务目标
type DemoService struct{}

// 演示任务目标
func NewDemo() *DemoService {
	return &DemoService{}
}

// 打印调用参数并返回执行结果
func (service *DemoService) Test(ctx context.Context, arguments []any) (any, error) {
	g.Log().Info(ctx, "演示任务被调用", "arguments", arguments)

	return "任务执行成功", nil
}
