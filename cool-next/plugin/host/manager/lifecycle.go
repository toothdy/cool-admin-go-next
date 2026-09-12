package manager

import (
	"context"
	"errors"
	"sort"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
)

// 本节点插件加载状态
type Diagnostic struct {
	Key             string
	DesiredRevision uint64
	LocalRevision   uint64
	SHA256          string
	State           string
	ErrorCode       protocol.ErrorCode
	Error           string
}

// 从 Store 加载当前期望启用的插件
func (manager *Manager) OnStart(ctx context.Context) error {
	if manager == nil {
		return errors.New("插件 Manager 不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	if manager.isClosed {
		manager.mu.Unlock()
		return errors.New("插件 Manager 已关闭")
	}
	if manager.isStarted {
		manager.mu.Unlock()
		return nil
	}
	if manager.store == nil {
		manager.mu.Unlock()
		return errors.New("插件 Manager Store 不可用")
	}
	syncCtx, cancelSync := context.WithCancel(ctx)
	syncDone := make(chan struct{})
	manager.isStarted = true
	manager.syncCancel = cancelSync
	manager.syncDone = syncDone
	manager.mu.Unlock()

	if err := manager.reconcile(ctx, true); err != nil {
		cancelSync()
		manager.mu.Lock()
		manager.isStarted = false
		manager.syncCancel = nil
		manager.syncDone = nil
		manager.mu.Unlock()

		return err
	}
	go manager.syncLoop(syncCtx, syncDone)

	return nil
}

// 停止新调用并排空全部插件实例
func (manager *Manager) OnStop(ctx context.Context) error {
	return manager.Close(ctx)
}

// 返回本节点插件状态快照
func (manager *Manager) Diagnostics() []Diagnostic {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]Diagnostic, 0, len(manager.diagnostics))
	for _, current := range manager.diagnostics {
		result = append(result, current)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Key < result[right].Key
	})

	return result
}

func (manager *Manager) setDiagnostic(desired DesiredPlugin, state string, currentErr error) {
	if manager == nil || desired.Key == "" {
		return
	}
	diagnostic := Diagnostic{
		Key:             desired.Key,
		DesiredRevision: desired.Revision,
		SHA256:          desired.SHA256,
		State:           state,
	}
	manager.mu.Lock()
	if previous, exists := manager.diagnostics[desired.Key]; exists {
		diagnostic.LocalRevision = previous.LocalRevision
	}
	if state == "enabled" || state == "disabled" {
		diagnostic.LocalRevision = desired.Revision
	}
	var pluginError *protocol.PluginError
	if errors.As(currentErr, &pluginError) && pluginError != nil {
		diagnostic.ErrorCode = pluginError.Code
		diagnostic.Error = pluginError.Message
	} else if currentErr != nil {
		diagnostic.Error = "插件加载失败"
	}
	manager.diagnostics[desired.Key] = diagnostic
	manager.mu.Unlock()
}
