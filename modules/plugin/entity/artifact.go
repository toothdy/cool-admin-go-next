package entity

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 不可变的插件安装制品
type Artifact struct {
	g.Meta `orm:"table:plugin_artifact" description:"插件制品"`
	gnentity.Base
	PluginID    uint64                     `json:"pluginId" orm:"pluginId" description:"插件 ID"`
	Version     string                     `json:"version" orm:"version" description:"版本" cool:"size=64"`
	SHA256      string                     `json:"sha256" orm:"sha256" description:"制品 SHA-256" cool:"size=64"`
	Size        int64                      `json:"size" orm:"size" description:"原始字节数"`
	Manifest    map[string]json.RawMessage `json:"manifest" orm:"manifest" description:"Manifest" cool:"json=true"`
	PackageData []byte                     `json:"packageData" orm:"packageData" description:"完整 COOL 制品" cool:"size=16777215"`
}

// 返回插件制品表索引
func ArtifactSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_plugin_artifact_plugin_id", "pluginId"),
		gnentity.UniqueIndexOf("uk_plugin_artifact_sha256", "sha256"),
	}}
}
