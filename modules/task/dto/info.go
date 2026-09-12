package dto

// 单个任务操作请求
type TaskRequest struct {
	ID uint64 `json:"id" v:"required|min:1"`
}

// 任务启动请求
type StartRequest struct {
	ID   uint64 `json:"id" v:"required|min:1"`
	Type *int32 `json:"type"`
}

// 任务日志分页请求
type LogRequest struct {
	ID     uint64 `json:"id" v:"required|min:1"`
	Status *int32 `json:"status"`
	Page   int    `json:"page" v:"required|min:1"`
	Size   int    `json:"size" v:"required|min:1"`
}
