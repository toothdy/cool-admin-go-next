package schedule

import (
	"context"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcron"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules/recycle"
	"github.com/toothdy/cool-admin-go-next/modules/recycle/service"
)

const dataCleanupJobName = "recycle-data-cleanup"

// 回收记录每日清理任务
type DataJob struct {
	service *service.DataService
	cleanup func(context.Context) (int64, error)
	cron    *gcron.Cron
	pattern string
	timeout time.Duration
	mu      sync.Mutex
	started bool
}

// 创建回收记录清理生命周期组件
func NewDataJob(data *service.DataService, config recycle.Config) (*DataJob, error) {
	if data == nil || config.Cleanup.Pattern == "" || config.Cleanup.Timeout <= 0 {
		return nil, exception.Core("回收记录清理配置无效")
	}

	return &DataJob{
		service: data,
		cleanup: data.ClearExpired,
		cron:    gcron.New(),
		pattern: config.Cleanup.Pattern,
		timeout: config.Cleanup.Timeout,
	}, nil
}

// 注册单例回收记录清理任务
func (job *DataJob) OnStart(ctx context.Context) error {
	if job == nil || job.cron == nil || job.service == nil || job.cleanup == nil {
		return exception.Core("回收记录清理任务未初始化")
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.started {
		return nil
	}
	if _, err := job.cron.AddSingleton(ctx, job.pattern, job.run, dataCleanupJobName); err != nil {
		return exception.WrapCore(err, "注册回收记录清理任务失败")
	}
	job.started = true

	return nil
}

// 移除任务并等待当前清理完成
func (job *DataJob) OnStop(ctx context.Context) error {
	if job == nil || job.cron == nil {
		return nil
	}
	job.mu.Lock()
	if !job.started {
		job.mu.Unlock()
		return nil
	}
	job.started = false
	job.cron.Remove(dataCleanupJobName)
	stopped := job.cron.StopGracefullyNonBlocking()
	job.mu.Unlock()

	select {
	case <-stopped.Done():
		job.cron.Close()
		return nil
	case <-ctx.Done():
		return exception.WrapCore(ctx.Err(), "等待回收记录清理任务停止失败")
	}
}

func (job *DataJob) run(ctx context.Context) {
	startedAt := time.Now()
	cleanupCtx, cancel := context.WithTimeout(ctx, job.timeout)
	defer cancel()
	g.Log().Info(cleanupCtx, "回收记录清理开始")
	count, err := job.cleanup(cleanupCtx)
	if err != nil {
		g.Log().Error(cleanupCtx, "回收记录清理失败", exception.LogText(err))
		return
	}
	g.Log().Info(cleanupCtx, "回收记录清理完成", "deleted", count, "elapsed", time.Since(startedAt))
}
