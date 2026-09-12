package dto

import (
	"encoding/json"

	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/gnrecycle"
)

// 回收记录分页请求
type DataPageRequest struct {
	Page    int    `json:"page"`
	Size    int    `json:"size"`
	KeyWord string `json:"keyWord"`
	Order   string `json:"order"`
	Sort    string `json:"sort"`
}

// 回收记录详情请求
type DataInfoRequest struct {
	ID uint64 `json:"id" v:"required|min:1"`
}

// 回收记录恢复请求
type DataRestoreRequest struct {
	IDs []uint64 `json:"ids" v:"required"`
}

// 回收记录目标实体信息
type EntityInfo struct {
	DataSourceName string `json:"dataSourceName"`
	Entity         string `json:"entity"`
}

// 回收记录项
type DataItem struct {
	ID         uint64          `json:"id"`
	CreateTime *gtime.Time     `json:"createTime"`
	UpdateTime *gtime.Time     `json:"updateTime"`
	Count      uint64          `json:"count"`
	Data       json.RawMessage `json:"data"`
	URL        string          `json:"url"`
	Params     json.RawMessage `json:"params"`
	UserID     *uint64         `json:"userId"`
	UserName   *string         `json:"userName"`
	EntityInfo EntityInfo      `json:"entityInfo"`
}

// 回收记录分页响应
type DataPageResult struct {
	List       []DataItem           `json:"list"`
	Pagination gnrecycle.Pagination `json:"pagination"`
}
