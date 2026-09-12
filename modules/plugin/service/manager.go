package service

import (
	"context"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/api"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/manager"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/host/runtime"
	"github.com/toothdy/cool-admin-go-next/modules/plugin"
)

// 创建插件模块唯一的运行时 Manager
func NewManager(store *Store, config plugin.Config) (*manager.Manager, error) {
	managerConfig := manager.DefaultConfig()
	managerConfig.Runtime = runtime.Config{
		MemoryLimitPages: config.MemoryLimitPages,
		MaxPayloadBytes:  config.MaxPayloadBytes,
		DataRoot:         config.DataRoot,
	}
	managerConfig.ArtifactLimits = bundle.Limits{
		MaxPackageBytes:  config.MaxPackageBytes,
		MaxUnpackedBytes: config.MaxUnpackedBytes,
		MaxEntries:       config.MaxEntries,
	}
	managerConfig.CallTimeout = config.CallTimeout
	managerConfig.InitTimeout = config.InitTimeout
	managerConfig.ShutdownTimeout = config.ShutdownTimeout
	managerConfig.SyncInterval = config.SyncInterval
	managerConfig.MaxInstancesPerPlugin = config.MaxInstancesPerPlugin
	managerConfig.MaxInstances = config.MaxInstances
	managerConfig.MaxCallDepth = config.MaxCallDepth
	hostFactory := func(invoker manager.Invoker) (runtime.HostHandler, error) {
		registry, err := api.New(api.Config{
			MaxPayloadBytes:      config.MaxPayloadBytes,
			HTTPTimeout:          config.CallTimeout,
			MaxHTTPRedirects:     5,
			MaxHTTPResponseBytes: int64(config.MaxPayloadBytes),
			DataRoot:             config.DataRoot,
		}, api.Dependencies{Invoker: invoker})
		if err != nil {
			return nil, err
		}

		return registry.Handle, nil
	}

	return manager.New(context.Background(), managerConfig, store, hostFactory)
}
