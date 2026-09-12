package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 任务状态
const (
	StatusStopped int32 = 0 // 停止
	StatusRunning int32 = 1 // 运行
)

// 定时规则类型
const (
	TaskTypeCron     int32 = 0 // cron 表达式
	TaskTypeInterval int32 = 1 // 时间间隔
)

// 任务信息
type Info struct {
	g.Meta `orm:"table:task_info" description:"任务信息"`
	gnentity.Base
	JobID           *string     `json:"jobId" orm:"jobId" description:"任务ID" cool:"size=255"`
	RepeatConf      *string     `json:"repeatConf" orm:"repeatConf" description:"任务配置" cool:"size=1000"`
	Name            string      `json:"name" orm:"name" description:"名称" cool:"size=255"`
	Cron            *string     `json:"cron" orm:"cron" description:"cron" cool:"size=255"`
	Limit           *int64      `json:"limit" orm:"limit" description:"最大执行次数 不传为无限次"`
	Every           *int64      `json:"every" orm:"every" description:"每间隔多少毫秒执行一次 如果cron设置了 这项设置就无效"`
	Remark          *string     `json:"remark" orm:"remark" description:"备注" cool:"size=255"`
	Status          int32       `json:"status" orm:"status" description:"状态 0-停止 1-运行" cool:"default=1"`
	StartDate       *gtime.Time `json:"startDate" orm:"startDate" description:"开始时间"`
	EndDate         *gtime.Time `json:"endDate" orm:"endDate" description:"结束时间"`
	Data            *string     `json:"data" orm:"data" description:"数据" cool:"size=255"`
	Service         *string     `json:"service" orm:"service" description:"执行的service实例ID" cool:"size=255"`
	Type            int32       `json:"type" orm:"type" description:"状态 0-系统 1-用户" cool:"default=0"`
	NextRunTime     *gtime.Time `json:"nextRunTime" orm:"nextRunTime" description:"下一次执行时间"`
	TaskType        int32       `json:"taskType" orm:"taskType" description:"状态 0-cron 1-时间间隔" cool:"default=0"`
	LastExecuteTime *gtime.Time `json:"lastExecuteTime" orm:"lastExecuteTime" description:"最近执行时间"`
	LockExpireTime  *gtime.Time `json:"lockExpireTime" orm:"lockExpireTime" description:"执行锁过期时间"`
	LockToken       *string     `json:"lockToken" orm:"lockToken" description:"执行锁令牌" cool:"size=64,default=0"`
}

// 任务信息表约束
func InfoSchema() gnentity.Schema {
	return gnentity.Schema{}
}
