package manager

import (
	"context"
	"encoding/json"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/bundle"
)

// Manager 使用的插件期望状态
type DesiredPlugin struct {
	ID         uint64
	Key        string
	Hook       string
	Singleton  bool
	Version    string
	Enabled    bool
	Manifest   bundle.Manifest
	Config     map[string]json.RawMessage
	RuntimeABI string
	ArtifactID uint64
	SHA256     string
	Revision   uint64
}

// 已校验且可供 Manager 加载的插件制品
type Artifact struct {
	ID       uint64
	PluginID uint64
	Version  string
	SHA256   string
	Size     int64
	Manifest bundle.Manifest
	Data     []byte
}

// Manager 同步插件期望状态所需的最小只读接口
type Store interface {
	ListDesired(ctx context.Context) ([]DesiredPlugin, error)
	LoadDesired(ctx context.Context, pluginID uint64) (DesiredPlugin, error)
	LoadArtifact(ctx context.Context, artifactID uint64) (Artifact, error)
}
