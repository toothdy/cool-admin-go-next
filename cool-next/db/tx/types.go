package tx

import (
	"context"

	"github.com/gogf/gf/v2/database/gdb"
)

// 事务回调
type Callback func(ctx context.Context) error

// 框架事务边界
type Runner interface {
	Group() string
	Within(ctx context.Context, callback Callback) error
}

type scopeContextKey struct{}

// 查询当前有效的框架事务
func Current(ctx context.Context) (transaction gdb.TX, group string, exists bool) {
	return currentScope(ctx)
}
