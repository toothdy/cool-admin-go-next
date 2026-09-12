package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
)

// 提交数据库前完成验证的一次性插件候选
type Candidate struct {
	mu         sync.Mutex
	manager    *Manager
	desired    DesiredPlugin
	generation *generation
	isFinished bool
}

// 重新校验制品并完成候选实例初始化
func (manager *Manager) Prepare(
	ctx context.Context,
	desired DesiredPlugin,
	stored Artifact,
) (*Candidate, error) {
	if manager == nil {
		return nil, errors.New("插件 Manager 不可用")
	}
	manager.mu.Lock()
	if manager.isClosed {
		manager.mu.Unlock()
		return nil, errors.New("插件 Manager 已关闭")
	}
	invocationID := manager.nextIDLocked()
	manager.mu.Unlock()

	parsed, err := bundle.ReadWithLimits(stored.Data, manager.config.ArtifactLimits)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorABIUnsupported, "读取插件候选制品失败", err)
	}
	if err = checkCandidate(desired, stored, parsed, manager.config.HostVersion); err != nil {
		return nil, err
	}
	configuration, err := json.Marshal(desired.Config)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorInvalidInput, "编码插件候选配置失败", err)
	}
	if desired.Config == nil {
		configuration = json.RawMessage(`{}`)
	}

	initCtx, cancelInit, err := manager.prepareContext(ctx, desired, invocationID, configuration)
	if err != nil {
		return nil, err
	}
	defer cancelInit()
	reference, err := manager.cache.acquire(initCtx, parsed.SHA256, parsed.Files[bundle.ModuleFile])
	if err != nil {
		return nil, err
	}
	instance, err := reference.compiled().InstantiateForPlugin(initCtx, desired.Key)
	if err != nil {
		return nil, errors.Join(err, manager.cache.release(context.Background(), reference))
	}
	if err = instance.Initialize(initCtx, invocationID, configuration); err != nil {
		return nil, errors.Join(
			err,
			instance.Close(context.Background()),
			manager.cache.release(context.Background(), reference),
		)
	}

	if desired.Enabled && desired.Singleton {
		current, createErr := manager.createGeneration(desired, reference, instance)
		if createErr != nil {
			return nil, errors.Join(
				createErr,
				stopCandidate(initCtx, manager.config.ShutdownTimeout, instance, invocationID),
				manager.cache.release(context.Background(), reference),
			)
		}

		return &Candidate{manager: manager, desired: cloneDesiredPlugin(desired), generation: current}, nil
	}

	if err = stopCandidate(initCtx, manager.config.ShutdownTimeout, instance, invocationID); err != nil {
		return nil, errors.Join(err, manager.cache.release(context.Background(), reference))
	}
	if !desired.Enabled {
		if err = manager.cache.release(context.Background(), reference); err != nil {
			return nil, err
		}

		return &Candidate{manager: manager, desired: cloneDesiredPlugin(desired)}, nil
	}

	current, err := manager.createGeneration(desired, reference, nil)
	if err != nil {
		return nil, errors.Join(err, manager.cache.release(context.Background(), reference))
	}

	return &Candidate{manager: manager, desired: cloneDesiredPlugin(desired), generation: current}, nil
}

// 原子发布候选
func (manager *Manager) Publish(candidate *Candidate) error {
	desired, current, err := candidate.consume(manager)
	if err != nil {
		return err
	}
	if !desired.Enabled {
		if err = manager.disable(desired.Key); err != nil {
			manager.setDiagnostic(desired, "error", err)

			return err
		}
		manager.setDiagnostic(desired, "disabled", nil)

		return nil
	}
	if current == nil {
		return errors.New("启用插件候选缺少 generation")
	}
	if err = manager.publish(current); err != nil {
		cleanupErr := current.shutdown(context.Background(), manager.nextInvocationID())
		manager.setDiagnostic(desired, "error", err)

		return errors.Join(err, cleanupErr)
	}
	manager.setDiagnostic(desired, "enabled", nil)

	return nil
}

// 原子发布候选并禁用占用相同 hook 的旧插件
func (manager *Manager) PublishReplacing(candidate *Candidate, disabled []DesiredPlugin) error {
	desired, current, err := candidate.consume(manager)
	if err != nil {
		return err
	}
	if !desired.Enabled || current == nil {
		cleanupErr := error(nil)
		if current != nil {
			cleanupErr = current.shutdown(context.Background(), manager.nextInvocationID())
		}

		return errors.Join(errors.New("替换发布需要启用插件候选"), cleanupErr)
	}

	manager.mu.Lock()
	if manager.isClosed {
		manager.mu.Unlock()
		cleanupErr := current.shutdown(context.Background(), manager.nextInvocationID())
		manager.setDiagnostic(desired, "error", errors.New("插件 Manager 已关闭"))

		return errors.Join(errors.New("插件 Manager 已关闭"), cleanupErr)
	}
	removed := make([]*generation, 0, len(disabled))
	for _, item := range disabled {
		if existing := manager.resolver.removeKey(item.Key); existing != nil {
			removed = append(removed, existing)
		}
	}
	previous, publishErr := manager.resolver.publish(current)
	if publishErr != nil {
		for _, existing := range removed {
			_, _ = manager.resolver.publish(existing)
		}
		manager.mu.Unlock()
		cleanupErr := current.shutdown(context.Background(), manager.nextInvocationID())
		manager.setDiagnostic(desired, "error", publishErr)

		return errors.Join(publishErr, cleanupErr)
	}
	manager.generations[current] = false
	manager.order = append(manager.order, current)
	if previous != nil && previous != current {
		manager.scheduleDrainLocked(previous)
	}
	for _, existing := range removed {
		if existing != previous {
			manager.scheduleDrainLocked(existing)
		}
	}
	manager.mu.Unlock()

	for _, item := range disabled {
		manager.setDiagnostic(item, "disabled", nil)
	}
	manager.setDiagnostic(desired, "enabled", nil)

	return nil
}

// 释放尚未发布的候选资源
func (candidate *Candidate) Close(ctx context.Context) error {
	if candidate == nil {
		return nil
	}
	candidate.mu.Lock()
	if candidate.isFinished {
		candidate.mu.Unlock()
		return nil
	}
	candidate.isFinished = true
	manager := candidate.manager
	current := candidate.generation
	candidate.mu.Unlock()
	if current == nil {
		return nil
	}
	invocationID := int64(1)
	if manager != nil {
		invocationID = manager.nextInvocationID()
	}

	return current.shutdown(ctx, invocationID)
}

func (candidate *Candidate) consume(manager *Manager) (DesiredPlugin, *generation, error) {
	if candidate == nil || manager == nil {
		return DesiredPlugin{}, nil, errors.New("插件候选无效")
	}
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.manager != manager {
		return DesiredPlugin{}, nil, errors.New("插件候选不属于当前 Manager")
	}
	if candidate.isFinished {
		return DesiredPlugin{}, nil, errors.New("插件候选已被消费或关闭")
	}
	candidate.isFinished = true

	return cloneDesiredPlugin(candidate.desired), candidate.generation, nil
}

func (manager *Manager) prepareContext(
	ctx context.Context,
	desired DesiredPlugin,
	invocationID int64,
	configuration json.RawMessage,
) (context.Context, context.CancelFunc, error) {
	initCtx, cancelInit := withCallTimeout(ctx, manager.config.InitTimeout)
	invocation, exists := InvocationFromContext(initCtx)
	if !exists {
		invocation = InvocationContext{Source: hostSource}
	}
	invocation.ID = invocationID
	initCtx = withInvocation(initCtx, invocation)
	initCtx, err := enterPlugin(initCtx, desired.Key, manager.config.MaxCallDepth)
	if err != nil {
		cancelInit()

		return nil, nil, err
	}
	initCtx = withGeneration(initCtx, GenerationMetadata{
		Key:     desired.Key,
		Version: desired.Version,
		Config:  append(json.RawMessage(nil), configuration...),
	})

	return initCtx, cancelInit, nil
}

func checkCandidate(
	desired DesiredPlugin,
	stored Artifact,
	parsed bundle.Package,
	hostVersion string,
) error {
	if err := desired.Manifest.Validate(); err != nil {
		return protocol.WrapError(protocol.ErrorABIUnsupported, "插件期望 Manifest 无效", err)
	}
	if err := parsed.Manifest.CheckHostVersion(hostVersion); err != nil {
		return protocol.WrapError(protocol.ErrorABIUnsupported, "插件宿主版本不兼容", err)
	}
	if desired.Key != parsed.Manifest.Key || desired.Hook != parsed.Manifest.Hook ||
		desired.Singleton != parsed.Manifest.Singleton || desired.Version != parsed.Manifest.Version ||
		desired.RuntimeABI != parsed.Manifest.Runtime.ABI {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件期望状态与 Manifest 不匹配")
	}
	if desired.SHA256 != parsed.SHA256 || stored.SHA256 != parsed.SHA256 || stored.Size != parsed.Size ||
		stored.Version != parsed.Manifest.Version {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件候选制品元数据不匹配")
	}
	if desired.ArtifactID != 0 && stored.ID != desired.ArtifactID {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件候选制品 ID 不匹配")
	}
	if desired.ID != 0 && stored.PluginID != desired.ID {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件候选所属插件不匹配")
	}
	if !reflect.DeepEqual(desired.Manifest, parsed.Manifest) || !reflect.DeepEqual(stored.Manifest, parsed.Manifest) {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件候选 Manifest 不匹配")
	}
	if module := parsed.Files[bundle.ModuleFile]; len(module) == 0 || !bytes.HasPrefix(module, []byte("\x00asm")) {
		return protocol.NewError(protocol.ErrorABIUnsupported, "插件候选缺少合法 WASM 模块")
	}

	return nil
}

func stopCandidate(
	ctx context.Context,
	timeoutDuration time.Duration,
	instance *runtime.Instance,
	invocationID int64,
) error {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), timeoutDuration)
	defer cancelShutdown()

	return errors.Join(instance.Shutdown(shutdownCtx, invocationID), instance.Close(context.Background()))
}
