package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 任务执行结果
const (
	LogStatusFail    int32 = 0 // 失败
	LogStatusSuccess int32 = 1 // 成功
)

// 任务日志
type Log struct {
	g.Meta `orm:"table:task_log" description:"任务日志"`
	gnentity.Base
	TaskID *uint64 `json:"taskId" orm:"taskId" description:"任务ID"`
	Status int32   `json:"status" orm:"status" description:"状态 0-失败 1-成功" cool:"default=0"`
	Detail *string `json:"detail" orm:"detail" description:"详情描述"`
}

// 任务日志表补充索引
func LogSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_task_log_task_id", "taskId"),
	}}
}
