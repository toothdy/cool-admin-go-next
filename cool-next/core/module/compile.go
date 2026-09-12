package module

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/config"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"

	"fmt"
)

// 已编译的模块声明和配置
type Compiled[T any] struct {
	identity          Identity
	name              string
	description       string
	order             int
	middlewares       []ComponentRef
	globalMiddlewares []ComponentRef
	config            *config.Result[T]
}

// 编译模块声明和配置
func Compile[T any](ctx context.Context, identity Identity, declaration Declaration[T], source config.Source) (*Compiled[T], error) {
	if err := checkIdentity(identity); err != nil {
		return nil, exception.WrapCore(err, "模块身份无效")
	}
	if err := checkDecl(declaration); err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("模块 %s 声明无效", identity.Key()))
	}

	cfg, err := config.Load(ctx, declaration.Defaults, source)
	if err != nil {
		return nil, exception.WrapCore(err, fmt.Sprintf("模块 %s 配置无效", identity.Key()))
	}

	return &Compiled[T]{
		identity:          identity,
		name:              declaration.Name,
		description:       declaration.Description,
		order:             declaration.Order,
		middlewares:       append([]ComponentRef(nil), declaration.Middlewares...),
		globalMiddlewares: append([]ComponentRef(nil), declaration.GlobalMiddlewares...),
		config:            cfg,
	}, nil
}

// 返回模块目录身份
func (c *Compiled[T]) Identity() Identity {
	if c == nil {
		return Identity{}
	}

	return c.identity
}

// 返回展示名称
func (c *Compiled[T]) Name() string {
	if c == nil {
		return ""
	}

	return c.name
}

// 返回模块说明
func (c *Compiled[T]) Description() string {
	if c == nil {
		return ""
	}

	return c.description
}

// 返回同层排序值
func (c *Compiled[T]) Order() int {
	if c == nil {
		return 0
	}

	return c.order
}

// 返回配置副本
func (c *Compiled[T]) Config() T {
	if c == nil || c.config == nil {
		var zero T
		return zero
	}

	return c.config.Value()
}

// 返回规范化配置 JSON 副本
func (c *Compiled[T]) ConfigJSON() []byte {
	if c == nil || c.config == nil {
		return nil
	}

	return c.config.CanonicalJSON()
}

// 返回模块中间件引用副本
func (c *Compiled[T]) Middlewares() []ComponentRef {
	if c == nil {
		return nil
	}

	return append([]ComponentRef(nil), c.middlewares...)
}

// 返回全局中间件引用副本
func (c *Compiled[T]) GlobalMiddlewares() []ComponentRef {
	if c == nil {
		return nil
	}

	return append([]ComponentRef(nil), c.globalMiddlewares...)
}
