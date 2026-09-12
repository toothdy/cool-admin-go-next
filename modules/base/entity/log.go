package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 后台业务操作日志
type Log struct {
	g.Meta `orm:"table:base_sys_log" description:"系统操作日志"`
	gnentity.Base
	UserID *uint64         `json:"userId" orm:"userId" description:"用户ID"`
	Action string          `json:"action" orm:"action" description:"行为" cool:"size=255"`
	IP     *string         `json:"ip" orm:"ip" description:"IP" cool:"size=255"`
	Params *map[string]any `json:"params" orm:"params" description:"参数" cool:"json=true"`
}

// 操作日志表补充索引
func LogSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.IndexOf("idx_base_sys_log_user_id", "userId"),
		gnentity.IndexOf("idx_base_sys_log_action", "action"),
		gnentity.IndexOf("idx_base_sys_log_ip", "ip"),
	}}
}
