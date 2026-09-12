package schedule

import (
	"context"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcron"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules/base"
	"github.com/toothdy/cool-admin-go-next/modules/base/service"
)

const logCleanupJobName = "base-operation-log-cleanup"

// 操作日志每日清理任务
type LogJob struct {
	service *service.LogService
	cleanup func(context.Context, bool) (int64, error)
	cron    *gcron.Cron
	pattern string
	timeout time.Duration
	mu      sync.Mutex
	started bool
}

// 操作日志清理生命周期组件
func NewLogJob(logService *service.LogService, config base.Config) (*LogJob, error) {
	if config.Log.CleanupPattern == "" || config.Log.CleanupTimeout <= 0 {
		return nil, exception.Core("操作日志清理配置无效")
	}

	return &LogJob{
		service: logService,
		cleanup: logService.Clear,
		cron:    gcron.New(),
		pattern: config.Log.CleanupPattern,
		timeout: config.Log.CleanupTimeout,
	}, nil
}

// 注册单例操作日志清理任务
func (job *LogJob) OnStart(ctx context.Context) error {
	if job == nil || job.cron == nil || job.service == nil || job.cleanup == nil {
		return exception.Core("操作日志清理任务未初始化")
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.started {
		return nil
	}
	if _, err := job.cron.AddSingleton(ctx, job.pattern, job.run, logCleanupJobName); err != nil {
		return exception.WrapCore(err, "注册操作日志清理任务失败")
	}
	job.started = true

	return nil
}

// 移除任务并等待当前清理完成
func (job *LogJob) OnStop(ctx context.Context) error {
	if job == nil || job.cron == nil {
		return nil
	}
	job.mu.Lock()
	if !job.started {
		job.mu.Unlock()
		return nil
	}
	job.started = false
	job.cron.Remove(logCleanupJobName)
	stopped := job.cron.StopGracefullyNonBlocking()
	job.mu.Unlock()

	select {
	case <-stopped.Done():
		job.cron.Close()
		return nil
	case <-ctx.Done():
		return exception.WrapCore(ctx.Err(), "等待操作日志清理任务停止失败")
	}
}

func (job *LogJob) run(ctx context.Context) {
	startedAt := time.Now()
	cleanupCtx, cancel := context.WithTimeout(ctx, job.timeout)
	defer cancel()
	g.Log().Info(cleanupCtx, "操作日志清理开始")
	count, err := job.cleanup(cleanupCtx, false)
	if err != nil {
		g.Log().Error(cleanupCtx, "操作日志清理失败", exception.LogText(err))
		return
	}
	g.Log().Info(cleanupCtx, "操作日志清理完成", "deleted", count, "elapsed", time.Since(startedAt))
}
