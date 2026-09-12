package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 公开参数配置
type Param struct {
	g.Meta `orm:"table:base_sys_param" description:"参数配置"`
	gnentity.Base
	KeyName  string  `json:"keyName" orm:"keyName" description:"键" cool:"size=255"`
	Name     string  `json:"name" orm:"name" description:"名称" cool:"size=255"`
	Data     string  `json:"data" orm:"data" description:"数据"`
	DataType int32   `json:"dataType" orm:"dataType" description:"数据类型 0-字符串 1-富文本 2-文件" cool:"default=0"`
	Remark   *string `json:"remark" orm:"remark" description:"备注" cool:"size=255"`
}

// 参数表补充索引
func ParamSchema() gnentity.Schema {
	return gnentity.Schema{Indexes: []gnentity.Index{
		gnentity.UniqueIndexOf("uk_base_sys_param_key_name", "keyName"),
	}}
}
