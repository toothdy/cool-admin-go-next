package app

import "context"

type readinessKey struct{}

// 应用就绪状态
type ReadyState interface {
	Ready() bool
}

// 协议无关的应用承载边界
type Transport interface {
	Name() string
	Prepare(context.Context) error
	Start(context.Context) (<-chan error, error)
	Stop(context.Context) error
}

// 返回 Host 注入的就绪状态
func Readiness(ctx context.Context) ReadyState {
	if ctx == nil {
		return unreadyState{}
	}
	if state, ok := ctx.Value(readinessKey{}).(ReadyState); ok {
		return state
	}
	return unreadyState{}
}

type unreadyState struct{}

// 始终未就绪的就绪状态
func (unreadyState) Ready() bool { return false }
