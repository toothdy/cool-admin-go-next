package service

import (
	"context"

	"github.com/gogf/gf/v2/os/gtime"
	"github.com/gogf/gf/v2/util/guid"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/modules/task/dto"
	"github.com/toothdy/cool-admin-go-next/modules/task/entity"
)

// 任务日志分页项
type LogItem struct {
	ID         uint64      `json:"id"`
	CreateTime *gtime.Time `json:"createTime"`
	UpdateTime *gtime.Time `json:"updateTime"`
	TaskID     *uint64     `json:"taskId"`
	TaskName   *string     `json:"taskName"`
	Status     int32       `json:"status"`
	Detail     *string     `json:"detail"`
}

// 任务日志分页结果
type LogResult struct {
	List       []LogItem            `json:"list"`
	Pagination gnservice.Pagination `json:"pagination"`
}

type statusWrite struct {
	Status int32 `orm:"status"`
}

type statusTypeWrite struct {
	Status int32 `orm:"status"`
	Type   int32 `orm:"type"`
}

// 任务信息 CRUD 与调度联动
type InfoService struct {
	*gnservice.Base[entity.Info, uint64]
	runtime   *db.Runtime
	logBase   *gnservice.Base[entity.Log, uint64]
	scheduler *Scheduler
}

// 任务信息业务服务
func NewInfo(
	runtime *db.Runtime,
	infoBase *gnservice.Base[entity.Info, uint64],
	logBase *gnservice.Base[entity.Log, uint64],
	scheduler *Scheduler,
) *InfoService {

	return &InfoService{Base: infoBase, runtime: runtime, logBase: logBase, scheduler: scheduler}
}

// 新增任务并按状态注册定时器
func (service *InfoService) Add(
	ctx context.Context,
	input gnservice.AddInput[entity.Info],
) (gnservice.AddResult[uint64], error) {
	for _, value := range addValues(input) {
		if err := prepareSchedule(value); err != nil {
			return gnservice.AddResult[uint64]{}, err
		}
		if !value.Has("jobId") || value.IsNull("jobId") {
			if err := value.Set("jobId", guid.S()); err != nil {
				return gnservice.AddResult[uint64]{}, err
			}
		}
	}
	result, err := service.Base.Add(ctx, input)
	if err != nil {
		return result, err
	}
	ids := result.Many()
	if !result.IsMany() {
		ids = []uint64{result.One()}
	}

	return result, service.syncAll(ctx, ids)
}

// 更新任务并重新注册定时器
func (service *InfoService) Update(ctx context.Context, input gnservice.UpdateInput[entity.Info, uint64]) error {
	items := input.Many()
	if !input.IsMany() {
		items = []gnservice.UpdateItem[entity.Info, uint64]{input.One()}
	}
	ids := make([]uint64, len(items))
	for index, item := range items {
		if err := prepareSchedule(item.Mutable()); err != nil {
			return err
		}
		ids[index] = item.ID()
	}
	if err := service.Base.Update(ctx, input); err != nil {
		return err
	}

	return service.syncAll(ctx, ids)
}

// 删除任务及其日志并移除定时器
func (service *InfoService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	ids := input.IDs()
	if err := service.Base.Delete(ctx, input); err != nil {
		return err
	}
	model, err := service.logBase.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.WhereIn("taskId", ids).Delete(); err != nil {
		return exception.WrapCore(err, "删除任务日志失败")
	}
	for _, id := range ids {
		service.scheduler.Remove(id)
	}

	return nil
}

// 开始任务
func (service *InfoService) Start(ctx context.Context, request *dto.StartRequest) error {
	var data any = statusWrite{Status: entity.StatusRunning}
	if request.Type != nil {
		data = statusTypeWrite{Status: entity.StatusRunning, Type: *request.Type}
	}
	if err := service.writeStatus(ctx, request.ID, data, "开始任务失败"); err != nil {
		return err
	}

	return service.scheduler.Sync(ctx, request.ID)
}

// 停止任务
func (service *InfoService) Stop(ctx context.Context, request *dto.TaskRequest) error {
	if err := service.writeStatus(ctx, request.ID, statusWrite{Status: entity.StatusStopped}, "停止任务失败"); err != nil {
		return err
	}
	service.scheduler.Remove(request.ID)

	return nil
}

// 立即执行一次
func (service *InfoService) Once(ctx context.Context, request *dto.TaskRequest) error {
	return service.scheduler.RunOnce(ctx, request.ID)
}

// 任务日志分页
func (service *InfoService) Log(ctx context.Context, request *dto.LogRequest) (LogResult, error) {
	model, err := service.logBase.Model(ctx)
	if err != nil {
		return LogResult{}, err
	}
	query := model.As("a").
		Fields("a.*", "b.name AS taskName").
		LeftJoin(service.Descriptor().Table(), "b", "a.taskId = b.id").
		Where("a.taskId", request.ID)
	if request.Status != nil {
		query = query.Where("a.status", *request.Status)
	}
	pageQuery, err := gnservice.NewQuery(nil, request.Page, request.Size)
	if err != nil {
		return LogResult{}, err
	}
	var items []LogItem
	pagination, err := service.logBase.EntityRenderPage(ctx, query.OrderDesc("a.id"), pageQuery, &items)
	if err != nil {
		return LogResult{}, err
	}

	return LogResult{
		List:       items,
		Pagination: pagination,
	}, nil
}

func (service *InfoService) writeStatus(ctx context.Context, taskID uint64, data any, message string) error {
	model, err := service.Model(ctx)
	if err != nil {
		return err
	}
	// 不用受影响行数判断存在性：值未变化时 MySQL 会返回 0
	exists, err := model.Where("id", taskID).Count()
	if err != nil {
		return exception.WrapCore(err, message)
	}
	if exists == 0 {
		return exception.Comm("任务不存在")
	}
	write, err := service.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = write.Where("id", taskID).Data(data).Update(); err != nil {
		return exception.WrapCore(err, message)
	}

	return nil
}

func (service *InfoService) syncAll(ctx context.Context, ids []uint64) error {
	for _, id := range ids {
		if err := service.scheduler.Sync(ctx, id); err != nil {
			return err
		}
	}

	return nil
}

func addValues(input gnservice.AddInput[entity.Info]) []*gnservice.Mutable[entity.Info] {
	if input.IsMany() {
		return input.Many()
	}

	return []*gnservice.Mutable[entity.Info]{input.One()}
}

// 准备任务定时器
func prepareSchedule(value *gnservice.Mutable[entity.Info]) error {
	if value == nil || !value.Has("taskType") {
		return nil
	}
	current, _ := value.Get("taskType")
	taskType, matches := current.(int32)
	if !matches {
		return exception.Validate("任务类型无效")
	}
	if taskType == entity.TaskTypeInterval {
		return value.SetNull("cron")
	}
	if err := value.SetNull("limit"); err != nil {
		return err
	}

	return value.SetNull("every")
}
