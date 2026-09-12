package gnrecycle

import (
	"context"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

const TableName = "cool_recycle" // 回收记录表名

// 回收记录不存在
var ErrRecordNotFound = gerror.New("回收记录不存在")

// 回收记录
type Record struct {
	g.Meta `orm:"table:cool_recycle" description:"删除回收记录"`
	gnentity.Base
	DatabaseGroup string  `json:"databaseGroup" orm:"databaseGroup" description:"数据库组" cool:"size=128"`
	TableName     string  `json:"tableName" orm:"tableName" description:"业务表名" cool:"size=128"`
	Data          []byte  `json:"data" orm:"data" description:"强类型快照"`
	Count         uint64  `json:"count" orm:"count" description:"记录数量"`
	Source        *string `json:"source" orm:"source" description:"脱敏来源" cool:"size=2048"`
	Params        *string `json:"params" orm:"params" description:"脱敏参数" cool:"size=4096"`
	OperatorType  *string `json:"operatorType" orm:"operatorType" description:"操作者类型" cool:"size=32"`
	OperatorID    *string `json:"operatorId" orm:"operatorId" description:"操作者 ID" cool:"size=128"`
}

// 回收记录分页条件
type PageInput struct {
	Page        int
	Size        int
	Keyword     string
	OperatorIDs []uint64
	Order       string
	Sort        string
}

// 回收记录分页信息
type Pagination struct {
	Page  int   `json:"page"`
	Size  int   `json:"size"`
	Total int64 `json:"total"`
}

// 回收记录分页结果
type PageResult struct {
	List       []Record   `json:"list"`
	Pagination Pagination `json:"pagination"`
}

// 删除归档入口
type Deleter interface {
	Delete(ctx context.Context, descriptor gnentity.RuntimeDescriptor, ids []any) error
}
