package dto

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gtime"
)

// 插件安装或升级请求
type InstallRequest struct {
	File  *ghttp.UploadFile `file:"files" v:"required"`
	Force bool              `form:"force"`
}

// 插件安装、升级或覆盖确认结果
type InstallResult struct {
	ID                uint64 `json:"id"`
	Key               string `json:"key"`
	Version           string `json:"version"`
	SHA256            string `json:"sha256"`
	Revision          uint64 `json:"revision"`
	NeedsConfirmation bool   `json:"needsConfirmation"`
}

// 插件配置或启用状态更新请求
type UpdateRequest struct {
	ID     uint64                      `json:"id" v:"required|min:1"`
	Status *int32                      `json:"status"`
	Config *map[string]json.RawMessage `json:"config"`
}

// 插件配置或启用状态更新请求解析器
func (request *UpdateRequest) UnmarshalJSON(data []byte) error {
	var value struct {
		ID     uint64          `json:"id"`
		Status *int32          `json:"status"`
		Config json.RawMessage `json:"config"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}

	request.ID = value.ID
	request.Status = value.Status
	request.Config = nil
	configData := bytes.TrimSpace(value.Config)
	if len(configData) == 0 || bytes.Equal(configData, []byte("null")) {
		return nil
	}
	if configData[0] == '"' {
		var encoded string
		if err := json.Unmarshal(configData, &encoded); err != nil {
			return err
		}
		configData = bytes.TrimSpace([]byte(encoded))
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(configData, &config); err != nil {
		return err
	}
	request.Config = &config

	return nil
}

// 插件期望状态更新结果
type UpdateResult struct {
	ID       uint64 `json:"id"`
	Key      string `json:"key"`
	Status   int32  `json:"status"`
	Revision uint64 `json:"revision"`
}

// 插件批量卸载请求
type DeleteRequest struct {
	IDs []uint64 `json:"ids" v:"required"`
}

// 插件详情查询请求
type InfoRequest struct {
	ID uint64 `json:"id" v:"required|min:1"`
}

// 插件详情响应
type InfoResult struct {
	ID          uint64                     `json:"id"`
	CreateTime  *gtime.Time                `json:"createTime"`
	UpdateTime  *gtime.Time                `json:"updateTime"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	KeyName     string                     `json:"keyName"`
	Hook        *string                    `json:"hook"`
	Singleton   bool                       `json:"singleton"`
	Readme      *string                    `json:"readme"`
	Version     string                     `json:"version"`
	Logo        []byte                     `json:"logo,omitempty"`
	Author      string                     `json:"author"`
	Status      int32                      `json:"status"`
	Config      map[string]json.RawMessage `json:"config,omitempty"`
	RuntimeABI  string                     `json:"runtimeABI"`
	Revision    uint64                     `json:"revision"`
}
