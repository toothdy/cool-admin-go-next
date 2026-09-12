package manager

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type syncFailure struct {
	revision uint64
	attempts uint
	retryAt  time.Time
}

func (manager *Manager) syncLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(manager.config.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = manager.reconcile(ctx, false)
		}
	}
}

func (manager *Manager) reconcile(ctx context.Context, force bool) error {
	desiredPlugins, err := manager.store.ListDesired(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(desiredPlugins))
	for _, summary := range desiredPlugins {
		if summary.Key == "" || summary.ID == 0 || summary.Revision == 0 {
			continue
		}
		seen[summary.Key] = true
		if summary.Enabled {
			continue
		}
		if !manager.shouldReconcile(summary, force, time.Now()) {
			continue
		}
		if err = manager.Disable(summary); err != nil {
			manager.recordSyncFailure(summary)
		} else {
			manager.clearSyncFailure(summary.Key)
		}
	}
	for _, key := range manager.localKeys() {
		if !seen[key] {
			_ = manager.Remove(key)
			manager.clearSyncFailure(key)
		}
	}
	for _, summary := range desiredPlugins {
		if !summary.Enabled || !manager.shouldReconcile(summary, force, time.Now()) {
			continue
		}
		if err = manager.loadAndPublish(ctx, summary); err != nil {
			manager.recordSyncFailure(summary)
		} else {
			manager.clearSyncFailure(summary.Key)
		}
	}

	return nil
}

func (manager *Manager) loadAndPublish(ctx context.Context, summary DesiredPlugin) error {
	desired, err := manager.store.LoadDesired(ctx, summary.ID)
	if err != nil {
		manager.setDiagnostic(summary, "error", err)

		return err
	}
	if desired.ID != summary.ID || desired.Key != summary.Key || desired.Revision != summary.Revision || !desired.Enabled {
		err = errors.New("插件期望状态在同步期间发生变化")
		manager.setDiagnostic(summary, "error", err)

		return err
	}
	stored, err := manager.store.LoadArtifact(ctx, desired.ArtifactID)
	if err != nil {
		manager.setDiagnostic(desired, "error", err)

		return err
	}
	candidate, err := manager.Prepare(ctx, desired, stored)
	if err != nil {
		manager.setDiagnostic(desired, "error", err)

		return err
	}
	if err = manager.Publish(candidate); err != nil {
		manager.setDiagnostic(desired, "error", err)

		return err
	}

	return nil
}

func (manager *Manager) shouldReconcile(summary DesiredPlugin, force bool, now time.Time) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.isClosed {
		return false
	}
	diagnostic, exists := manager.diagnostics[summary.Key]
	if exists && diagnostic.LocalRevision == summary.Revision {
		if summary.Enabled && diagnostic.State == "enabled" || !summary.Enabled && diagnostic.State == "disabled" {
			return false
		}
	}
	if force {
		return true
	}
	failure, exists := manager.syncFailures[summary.Key]
	return !exists || failure.revision != summary.Revision || !now.Before(failure.retryAt)
}

func (manager *Manager) recordSyncFailure(summary DesiredPlugin) {
	manager.mu.Lock()
	failure := manager.syncFailures[summary.Key]
	if failure.revision != summary.Revision {
		failure = syncFailure{revision: summary.Revision}
	}
	failure.attempts++
	failure.retryAt = time.Now().Add(syncBackoff(manager.config.SyncInterval, failure.attempts))
	manager.syncFailures[summary.Key] = failure
	manager.mu.Unlock()
}

func (manager *Manager) clearSyncFailure(key string) {
	manager.mu.Lock()
	delete(manager.syncFailures, key)
	manager.mu.Unlock()
}

func (manager *Manager) localKeys() []string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	keys := make([]string, 0, len(manager.diagnostics))
	for key := range manager.diagnostics {
		keys = append(keys, key)
	}

	return keys
}

func (manager *Manager) stopSync(ctx context.Context) error {
	manager.mu.Lock()
	cancel := manager.syncCancel
	done := manager.syncDone
	manager.syncCancel = nil
	manager.syncDone = nil
	manager.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待插件同步停止失败: %w", ctx.Err())
	}
}

func syncBackoff(interval time.Duration, attempts uint) time.Duration {
	delay := interval
	maximum := time.Minute
	if interval > maximum {
		maximum = interval
	}
	for current := uint(1); current < attempts && delay < maximum; current++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}

	return delay
}
