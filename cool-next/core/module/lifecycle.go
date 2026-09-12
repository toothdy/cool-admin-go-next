package module

import (
	"context"
)

// 组件初始化契约
type Initializer interface {
	OnInit(context.Context) error
}

// 组件启动契约
type Starter interface {
	OnStart(context.Context) error
}

// 组件停止契约
type Stopper interface {
	OnStop(context.Context) error
}

// 组件运行循环监督契约
type Supervisor interface {
	Terminated() <-chan error
}

// 组件生命周期静态定义
type LifecycleDefinition struct {
	Component   ComponentDefinition // 目标组件
	Initializer bool                // 初始化能力
	Starter     bool                // 启动能力
	Stopper     bool                // 停止能力
	Supervisor  bool                // 运行监督能力
}
