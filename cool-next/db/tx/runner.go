package tx

import (
	"context"
	"errors"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"

	"fmt"
)

type runner struct {
	group    string
	database gdb.DB // 数据库对象
}

// 创建框架事务边界
func NewRunner(group string, database gdb.DB) (Runner, error) {
	if strings.TrimSpace(group) == "" {
		return nil, exception.Core("数据库组不能为空")
	}
	if database == nil {
		return nil, exception.Core("数据库对象不能为 nil")
	}
	if database.GetGroup() != group {
		return nil, exception.Core(fmt.Sprintf("数据库组不匹配: 期望 %s，实际 %s", group, database.GetGroup()))
	}

	return &runner{group: group, database: database}, nil
}

// 返回事务数据库组
func (r *runner) Group() string {
	return r.group
}

// 在框架事务中执行回调
func (r *runner) Within(ctx context.Context, callback Callback) (err error) {
	if callback == nil {
		return exception.Core("事务回调不能为 nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, currentGroup, exists := Current(ctx); exists {
		if currentGroup != r.group {
			return exception.Core(fmt.Sprintf("事务数据库组不匹配: 当前 %s，请求 %s", currentGroup, r.group))
		}

		err = callback(ctx)
		recordFailure(ctx, err)
		return err
	}

	transaction, err := r.database.Begin(ctx)
	if err != nil {
		return err
	}
	scope := newScope(r.group, transaction)
	scopeCtx := context.WithValue(ctx, scopeContextKey{}, scope)

	defer func() {
		if recovered := recover(); recovered != nil {
			scope.markRollback()
			scope.close()
			_ = transaction.Rollback()
			panic(recovered)
		}

		scope.recordFailure(err)
		failure := scope.failure()
		scope.close()
		if failure != nil {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				err = errors.Join(failure, rollbackErr)
				return
			}
			err = failure
			return
		}
		err = transaction.Commit()
	}()

	err = callback(scopeCtx)
	return err
}

func recordFailure(ctx context.Context, err error) {
	if ctx == nil {
		return
	}
	scope, ok := ctx.Value(scopeContextKey{}).(*scope)
	if !ok {
		return
	}

	scope.recordFailure(err)
}
