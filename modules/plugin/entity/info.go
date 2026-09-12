package entity

import (
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

const (
	StatusDisabled int32 = 0
	StatusEnabled  int32 = 1
)

// 插件期望状态和当前制品快照
type Info struct {
	g.Meta `orm:"table:plugin_info" description:"插件信息"`
	gnentity.Base
	Name             string                     `json:"name" orm:"name" description:"名称" cool:"size=100"`
	Description      string                     `json:"description" orm:"description" description:"描述" cool:"size=500"`
	KeyName          string                     `json:"keyName" orm:"keyName" description:"唯一标识" cool:"size=64"`
	Hook             *string                    `json:"hook" orm:"hook" description:"业务挂载点" cool:"size=64"`
	Singleton        bool                       `json:"singleton" orm:"singleton" description:"是否使用单例"`
	Readme           *string                    `json:"readme" orm:"readme" description:"使用说明"`
	Version          string                     `json:"version" orm:"version" description:"版本" cool:"size=64"`
	Logo             []byte                     `json:"logo" orm:"logo" description:"图标"`
	Author           string                     `json:"author" orm:"author" description:"作者" cool:"size=100"`
	Status           int32                      `json:"status" orm:"status" description:"状态 0-禁用 1-启用" cool:"default=1"`
	Manifest         map[string]json.RawMessage `json:"manifest" orm:"manifest" description:"当前 Manifest" cool:"json=true"`
	PluginJSON       map[string]json.RawMessage `json:"pluginJson" description:"插件 Manifest 兼容字段" cool:"json=true,transient"`
	Config           map[string]json.RawMessage `json:"config" orm:"config" description:"管理员原始配置" cool:"json=true"`
	RuntimeABI       string                     `json:"runtimeABI" orm:"runtimeABI" description:"运行时 ABI" cool:"size=64"`
	ActiveArtifactID uint64                     `json:"activeArtifactID" orm:"activeArtifactID" description:"当前制品 ID"`
	Revision         uint64                     `json:"revision" orm:"revision" description:"期望状态版本"`
}

// 返回插件信息表索引
func InfoSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.UniqueIndexOf("uk_plugin_info_key_name", "keyName"),
		gnentity.IndexOf("idx_plugin_info_hook", "hook"),
		gnentity.IndexOf("idx_plugin_info_active_artifact_id", "activeArtifactID"),
	}}
}
