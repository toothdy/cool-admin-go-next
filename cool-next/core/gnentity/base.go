package gnentity

import "github.com/gogf/gf/v2/os/gtime"

// 所有业务实体共享的固定字段
type Base struct {
	ID         uint64      `json:"id" orm:"id" description:"ID"`
	CreateTime *gtime.Time `json:"createTime" orm:"createTime" description:"创建时间"`
	UpdateTime *gtime.Time `json:"updateTime" orm:"updateTime" description:"更新时间"`
}
