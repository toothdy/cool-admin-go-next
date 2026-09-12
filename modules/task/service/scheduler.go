package service

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcron"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/task/entity"
)

type scheduleRow struct {
	ID       uint64  `orm:"id"`
	JobID    *string `orm:"jobId"`
	Name     string  `orm:"name"`
	Status   int32   `orm:"status"`
	TaskType int32   `orm:"taskType"`
	Cron     *string `orm:"cron"`
	Every    *int64  `orm:"every"`
}

type nextRunTimeWrite struct {
	NextRunTime *gtime.Time `orm:"nextRunTime"`
}

// 任务定时器注册表
type Scheduler struct {
	cron     *gcron.Cron
	executor *Executor
	infoBase *gnservice.Base[entity.Info, uint64]
	mu       sync.Mutex
	entries  map[uint64]string
	sequence atomic.Uint64
}

// 任务定时器
func NewScheduler(
	executor *Executor,
	infoBase *gnservice.Base[entity.Info, uint64],
) *Scheduler {

	return &Scheduler{
		cron:     gcron.New(),
		executor: executor,
		infoBase: infoBase,
		entries:  make(map[uint64]string),
	}
}

// 装载全部运行中的任务
func (scheduler *Scheduler) Load(ctx context.Context) error {
	model, err := scheduler.infoBase.Model(ctx)
	if err != nil {
		return err
	}
	var rows []scheduleRow
	if err = model.
		Fields(scheduleFields...).
		Where("status", entity.StatusRunning).
		OrderAsc("id").
		Scan(&rows); err != nil {
		return exception.WrapCore(err, "查询运行中的任务失败")
	}
	for _, row := range rows {
		if err = scheduler.register(ctx, row); err != nil {
			g.Log().Error(ctx, "装载任务失败", "task", row.Name, exception.LogText(err))
		}
	}

	return nil
}

// 移除全部条目并等待在跑的任务结束
func (scheduler *Scheduler) Shutdown(ctx context.Context) error {
	if scheduler == nil || scheduler.cron == nil {
		return nil
	}
	scheduler.mu.Lock()
	for _, name := range scheduler.entries {
		scheduler.cron.Remove(name)
	}
	clear(scheduler.entries)
	stopped := scheduler.cron.StopGracefullyNonBlocking()
	scheduler.mu.Unlock()

	select {
	case <-stopped.Done():
		scheduler.cron.Close()
		return nil
	case <-ctx.Done():
		return exception.WrapCore(ctx.Err(), "等待任务定时器停止失败")
	}
}

// 按任务当前状态注册或移除条目并刷新下次执行时间
func (scheduler *Scheduler) Sync(ctx context.Context, taskID uint64) error {
	model, err := scheduler.infoBase.Model(ctx)
	if err != nil {
		return err
	}
	var row *scheduleRow
	if err = model.Fields(scheduleFields...).Where("id", taskID).Scan(&row); err != nil {
		return exception.WrapCore(err, "查询任务失败")
	}
	if row == nil || row.Status != entity.StatusRunning {
		scheduler.Remove(taskID)

		return nil
	}

	return scheduler.register(ctx, *row)
}

// 移除任务条目
func (scheduler *Scheduler) Remove(taskID uint64) {
	if scheduler == nil || scheduler.cron == nil {
		return
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if name, exists := scheduler.entries[taskID]; exists {
		scheduler.cron.Remove(name)
		delete(scheduler.entries, taskID)
	}
}

// 立即执行一次，不改变任务状态与下次执行时间
func (scheduler *Scheduler) RunOnce(ctx context.Context, taskID uint64) error {
	return scheduler.executor.ExecuteOnce(ctx, taskID)
}

var scheduleFields = []any{"id", "jobId", "name", "status", "taskType", "cron", "every"}

func (scheduler *Scheduler) register(ctx context.Context, row scheduleRow) error {
	schedule, err := CompileSchedule(row.TaskType, row.Cron, row.Every)
	if err != nil {
		return err
	}
	name := scheduler.entryName(row)
	scheduler.Remove(row.ID)
	scheduler.mu.Lock()
	// gcron 触发时复用注册期 Context，用请求事务 Context 注册会让任务运行在已关闭的事务上
	_, err = scheduler.cron.AddSingleton(context.Background(), schedule.Pattern(), func(jobCtx context.Context) {
		scheduler.run(jobCtx, row.ID, row.Name)
	}, name)
	if err == nil {
		scheduler.entries[row.ID] = name
	}
	scheduler.mu.Unlock()
	if err != nil {
		return exception.WrapComm(err, "任务定时规则无效")
	}

	return scheduler.updateNextRunTime(ctx, row.ID, schedule)
}

func (scheduler *Scheduler) run(ctx context.Context, taskID uint64, name string) {
	outcome, err := scheduler.executor.Execute(ctx, taskID)
	if err != nil {
		g.Log().Error(ctx, "任务执行失败", "task", name, exception.LogText(err))

		return
	}
	// 业务事务回滚或其他实例改动会让条目残留，执行入口在此自注销
	if outcome.Removed {
		scheduler.Remove(taskID)

		return
	}
	if !outcome.Executed {
		return
	}
	// 只刷新下次执行时间：回调内重新注册会让 gcron 在本次回调结束时把新条目一并注销，
	// 旧条目却继续在 gtimer 上走，最终每执行一次就多出一个条目
	if err = scheduler.refreshNextRunTime(ctx, taskID); err != nil {
		g.Log().Error(ctx, "刷新任务下次执行时间失败", "task", name, exception.LogText(err))
	}
}

func (scheduler *Scheduler) refreshNextRunTime(ctx context.Context, taskID uint64) error {
	model, err := scheduler.infoBase.Model(ctx)
	if err != nil {
		return err
	}
	var row *scheduleRow
	if err = model.Fields(scheduleFields...).Where("id", taskID).Scan(&row); err != nil {
		return exception.WrapCore(err, "查询任务失败")
	}
	if row == nil || row.Status != entity.StatusRunning {
		return nil
	}
	schedule, err := CompileSchedule(row.TaskType, row.Cron, row.Every)
	if err != nil {
		return err
	}

	return scheduler.updateNextRunTime(ctx, taskID, schedule)
}

func (scheduler *Scheduler) updateNextRunTime(ctx context.Context, taskID uint64, schedule *Schedule) error {
	next, exists := schedule.Next(time.Now())
	if !exists {
		return nil
	}
	model, err := scheduler.infoBase.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.Where("id", taskID).Data(nextRunTimeWrite{NextRunTime: gtime.New(next)}).Update(); err != nil {
		return exception.WrapCore(err, "更新任务下次执行时间失败")
	}

	return nil
}

// 条目名每次注册都取新值：gcron 关闭一个条目时按名字从注册表里删除，
// 复用同名会让正在跑的旧条目在收尾时把刚注册的新条目一并删掉
func (scheduler *Scheduler) entryName(row scheduleRow) string {
	identity := "task-" + strconv.FormatUint(row.ID, 10)
	if row.JobID != nil && *row.JobID != "" {
		identity = *row.JobID
	}

	return identity + "#" + strconv.FormatUint(scheduler.sequence.Add(1), 10)
}
