package schedule

import (
	"context"
	"sync"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules/task/service"
)

// 任务定时器生命周期组件
type TaskJob struct {
	scheduler *service.Scheduler
	mu        sync.Mutex
	started   bool
}

// 任务调度生命周期组件
func NewTaskJob(scheduler *service.Scheduler) *TaskJob {

	return &TaskJob{scheduler: scheduler}
}

// 装载全部运行中的任务
func (job *TaskJob) OnStart(ctx context.Context) error {
	if job == nil || job.scheduler == nil {
		return exception.Core("任务调度组件未初始化")
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.started {
		return nil
	}
	if err := job.scheduler.Load(ctx); err != nil {
		return err
	}
	job.started = true

	return nil
}

// 停止定时器并等待在跑的任务结束
func (job *TaskJob) OnStop(ctx context.Context) error {
	if job == nil || job.scheduler == nil {
		return nil
	}
	job.mu.Lock()
	if !job.started {
		job.mu.Unlock()

		return nil
	}
	job.started = false
	job.mu.Unlock()

	return job.scheduler.Shutdown(ctx)
}
