package tx

import (
	"context"
	"sync"

	"github.com/gogf/gf/v2/database/gdb"
)

// 单次事务生命周期
type scope struct {
	group    string
	tx       gdb.TX
	mutex    sync.Mutex
	closed   bool
	rollback bool
	firstErr error
}

func newScope(group string, transaction gdb.TX) *scope {
	return &scope{group: group, tx: transaction}
}

func currentScope(ctx context.Context) (transaction gdb.TX, group string, exists bool) {
	if ctx == nil {
		return nil, "", false
	}
	scope, ok := ctx.Value(scopeContextKey{}).(*scope)
	if !ok {
		return nil, "", false
	}
	scope.mutex.Lock()
	defer scope.mutex.Unlock()
	if scope.closed {
		return nil, "", false
	}

	return scope.tx, scope.group, true
}

func (s *scope) recordFailure(err error) {
	if err == nil {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.rollback = true
	if s.firstErr == nil {
		s.firstErr = err
	}
}

func (s *scope) markRollback() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.rollback = true
}

func (s *scope) failure() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if !s.rollback {
		return nil
	}

	return s.firstErr
}

func (s *scope) close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.closed = true
}
