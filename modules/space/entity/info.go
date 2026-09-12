package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 文件空间信息
type Info struct {
	g.Meta `orm:"table:space_info" description:"文件空间信息"`
	gnentity.Base
	URL        string  `json:"url" orm:"url" description:"地址" cool:"size=255"`
	Type       string  `json:"type" orm:"type" description:"类型" cool:"size=255"`
	ClassifyID *uint64 `json:"classifyId" orm:"classifyId" description:"分类ID"`
	FileID     string  `json:"fileId" orm:"fileId" description:"文件ID" cool:"size=255"`
	Name       string  `json:"name" orm:"name" description:"文件名" cool:"size=255"`
	Size       int32   `json:"size" orm:"size" description:"文件大小"`
	Version    int32   `json:"version" orm:"version" description:"文档版本" cool:"default=1"`
	Key        string  `json:"key" orm:"key" description:"文件位置" cool:"size=255"`
	UID        int64   `json:"uid" description:"上传临时ID" cool:"transient"`
	Progress   float64 `json:"progress" description:"上传进度" cool:"transient"`
	Preload    string  `json:"preload" description:"预览地址" cool:"transient"`
	Error      string  `json:"error" description:"上传错误" cool:"transient"`
}

// 文件空间信息表约束
func InfoSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_space_info_file_id", "fileId"),
	}}
}
