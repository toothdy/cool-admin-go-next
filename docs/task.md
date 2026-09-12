# 任务开发

框架支持两种任务：

- **代码定时任务**：执行规则写在代码中，随应用发布。
- **后台配置任务**：执行规则在管理后台配置，可以动态启停。

固定的数据清理、同步任务优先使用代码定时任务。需要管理员调整执行周期或手动触发时，使用后台配置任务。

## 1. 代码定时任务

### 1.1 创建任务组件

在业务模块的 `schedule` 目录创建任务：

```text
modules/shop/
├── service/
│   └── log.go
└── schedule/
    └── cleanup.go
```

```go
package schedule

import (
   "context"

   "github.com/gogf/gf/v2/frame/g"
   "github.com/gogf/gf/v2/os/gcron"
   "github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
   "github.com/toothdy/cool-admin-go-next/modules/shop/service"
)

// 日志清理定时任务
type CleanupJob struct {
   logService *service.LogService
   cron       *gcron.Cron
}

// 创建日志清理任务
func NewCleanupJob(logService *service.LogService) *CleanupJob {
   return &CleanupJob{
      logService: logService,
      cron:       gcron.New(),
   }
}

// 注册定时任务
func (job *CleanupJob) OnStart(ctx context.Context) error {
   _, err := job.cron.AddSingleton(
      ctx,
      "0 0 3 * * *",
      job.run,
      "shop-log-cleanup",
   )
   if err != nil {
      return exception.WrapCore(err, "注册日志清理任务失败")
   }

   return nil
}

// 停止定时任务
func (job *CleanupJob) OnStop(ctx context.Context) error {
   job.cron.Remove("shop-log-cleanup")
   stopped := job.cron.StopGracefullyNonBlocking()

   select {
   case <-stopped.Done():
      job.cron.Close()
      return nil
   case <-ctx.Done():
      return exception.WrapCore(ctx.Err(), "等待日志清理任务停止失败")
   }
}

func (job *CleanupJob) run(ctx context.Context) {
   if err := job.logService.Clear(ctx); err != nil {
      g.Log().Error(ctx, "日志清理失败", exception.LogText(err))
   }
}
```

新增任务组件后执行：

```bash
go run ./cmd/cool generate
go run ./cmd/cool check
```

不要手动修改生成的 `modules/modules.go`。

### 1.2 开发约定

- 使用 `AddSingleton` 避免同一进程内任务重叠执行。
- 每个任务使用稳定且唯一的名称，例如 `shop-log-cleanup`。
- 在 `OnStop` 中移除任务并等待正在执行的回调结束。
- 业务错误需要记录日志，不能直接忽略。
- 长任务应检查 `ctx.Done()`，及时响应应用关闭。
- 多实例部署时，每个实例都会执行代码定时任务。集群中只能执行一次的任务应使用后台配置任务。

### 1.3 cron 表达式

GoFrame 使用 6 段 cron：

```text
秒 分 时 日 月 周
```

常用表达式：

| 表达式 | 执行时间 |
| --- | --- |
| `0 */5 * * * *` | 每 5 分钟 |
| `0 0 3 * * *` | 每天 03:00 |
| `0 0 9 * * MON` | 每周一 09:00 |
| `@hourly` | 每小时 |
| `@every 30m` | 每 30 分钟 |

代码任务支持 GoFrame 的完整 cron 语法，包括 `*`、`/`、`,`、`-`、`?`、英文月份和星期，以及 `@daily`、`@hourly`、`@every`。

cron 使用应用进程的全局时区；未设置时使用系统时区。

## 2. 后台配置任务

后台配置任务分为三步：

1. 在自己的业务模块中编写任务方法。
2. 在任务注册表中登记方法。
3. 在管理后台填写执行规则和调用字符串。

### 2.1 编写任务方法

以日报模块为例，在自己的模块中创建任务 Service：

```text
modules/report/service/task.go
```

```go
package service

import (
   "context"

   "github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 报表后台任务
type ReportTask struct {
   reportService *ReportService
}

// 创建报表后台任务
func NewReportTask(reportService *ReportService) *ReportTask {
   return &ReportTask{reportService: reportService}
}

// 生成指定日期的日报
func (task *ReportTask) BuildDaily(
   ctx context.Context,
   arguments []any,
) (any, error) {
   if len(arguments) != 1 {
      return nil, exception.Validate("日报任务需要一个日期参数")
   }

   date, isDate := arguments[0].(string)
   if !isDate || date == "" {
      return nil, exception.Validate("日报日期参数无效")
   }

   return task.reportService.BuildDaily(ctx, date)
}
```

后台任务方法必须符合以下签名：

```go
func(context.Context, []any) (any, error)
```

`arguments` 来自管理后台，必须校验参数数量和类型。JSON 数字会解析为 `float64`，不要直接断言为 `int`。

任务返回值会编码为 JSON 并写入成功日志；返回错误会写入失败日志。

### 2.2 注册任务方法

当前任务注册表位于：

```text
modules/task/service/registry.go
```

将业务任务 Service 注入 `NewRegistry`，然后登记调用名称：

```go
import (
   reportservice "github.com/toothdy/cool-admin-go-next/modules/report/service"
)

func NewRegistry(
   demo *DemoService,
   reportTask *reportservice.ReportTask,
) *Registry {
   return &Registry{callables: map[string]Callable{
      "taskDemoService.test":     demo.Test,
      "reportService.buildDaily": reportTask.BuildDaily,
   }}
}
```

注册名称必须：

- 使用且只使用一个点，例如 `reportService.buildDaily`。
- 在项目中保持唯一。
- 发布后保持稳定，否则已有后台任务会找不到调用目标。

修改注册表构造函数后执行：

```bash
go run ./cmd/cool generate
go run ./cmd/cool check
```

### 2.3 配置后台任务

在管理后台新增任务时，主要填写以下字段：

| 字段 | 说明 |
| --- | --- |
| 名称 | 便于识别的任务名称 |
| 任务类型 | cron 或固定间隔 |
| cron | 5 段或 6 段表达式 |
| 执行间隔 | 固定间隔的毫秒数，最小 1000 |
| Service | 已注册的任务调用字符串 |
| 状态 | 运行或停止 |
| 开始时间 | 可选，早于该时间不会执行 |
| 结束时间 | 可选，晚于该时间不会执行 |

日报任务示例：

```text
名称：每日生成报表
任务类型：cron
cron：0 0 2 * * *
Service：reportService.buildDaily("2026-09-12")
状态：运行
```

固定间隔任务示例：

```text
名称：每五分钟同步
任务类型：固定间隔
执行间隔：300000
Service：taskDemoService.test()
状态：运行
```

## 3. 调用参数

调用字符串格式：

```text
注册名称(参数1,参数2)
```

支持字符串、数字、布尔值、`null`、数组和对象：

```text
reportService.buildDaily("2026-09-12")
taskDemoService.test(10, true)
taskDemoService.test({"region":"cn"}, [1,2,3])
taskDemoService.test()
```

参数规则：

- 字符串推荐使用 JSON 双引号。
- 数组或对象内部的逗号不会分隔参数。
- 数字会解析为 `float64`。
- 空参数列表合法。
- 连续逗号、空参数、未闭合引号或括号会校验失败。
- 注册名称或参数发生变化时，需要同步更新已有后台任务。

## 4. 后台任务的 cron

后台任务支持 5 段或 6 段表达式：

```text
分 时 日 月 周
秒 分 时 日 月 周
```

5 段表达式会自动补充秒字段 `0`。

| 表达式 | 执行时间 |
| --- | --- |
| `*/5 * * * *` | 每 5 分钟 |
| `0/5 * * * * *` | 每 5 秒 |
| `0 0 2 * * *` | 每天 02:00 |
| `0 30 9 * * 1-5` | 周一至周五 09:30 |

后台任务建议使用数字字段和标准的 5 段或 6 段表达式，不使用英文星期、英文月份或 `@daily` 等预定义格式，以保证“下次执行时间”能够正确计算。

固定间隔任务的 `every` 单位为毫秒，实际精度为整秒。建议填写 1000 的整数倍，例如：

```text
1000      每秒
60000     每分钟
300000    每五分钟
```

## 5. 运行规则

开发后台任务时需要了解以下行为：

- **防止重叠**：同一个任务尚未结束时，下一次定时触发会被跳过。
- **多实例协调**：后台任务使用数据库租约，同一时间只有一个应用实例执行。
- **仍需幂等**：进程崩溃或外部请求超时后，任务可能再次执行。涉及扣款、消息发送等操作时必须使用业务唯一键或幂等接口。
- **没有自动重试**：方法返回错误后只记录失败日志，不会自动重试。
- **停止任务**：停止只会阻止后续触发，不会取消已经开始的执行。
- **立即执行**：停止状态的任务也可以手动执行一次，但仍受开始时间、结束时间和执行中状态限制。
- **上下文取消**：长任务应响应 `ctx.Done()`，避免应用关闭或执行权丢失后继续处理。
- **执行日志**：成功日志保存返回值的 JSON，失败日志保存错误信息。

代码定时任务的 `AddSingleton` 只在当前进程生效；后台配置任务的数据库租约才用于多实例协调。

## 6. 常见问题

### 任务没有执行

依次检查：

1. 任务状态是否为运行。
2. cron 或执行间隔是否正确。
3. Service 注册名称是否完全一致。
4. 当前时间是否在开始和结束时间范围内。
5. 应用日志是否出现“任务调用目标不存在”或“任务定时规则无效”。

### 报“任务调用目标不存在”

检查 `modules/task/service/registry.go` 中是否已注册该名称。修改注册表后重新执行生成和检查命令。

### 立即执行成功但没有执行日志

任务可能已经在执行，或当前时间不在允许的执行范围内。立即执行遇到这两种情况会跳过本次调用。

### 多实例重复执行

确认使用的是后台配置任务，并且所有实例连接同一个数据库。任务方法本身仍然必须保证幂等。

### 任务失败后没有重试

当前任务执行器不提供自动重试。需要重试时，在业务方法中实现有限重试，或设计单独的补偿任务。

## 7. 开发检查清单

- [ ] 任务代码位于自己的业务模块中。
- [ ] 后台任务方法校验了参数数量和类型。
- [ ] 后台任务已注册，调用名称稳定且唯一。
- [ ] cron 或固定间隔配置正确。
- [ ] 任务方法能够响应 `context.Context` 取消。
- [ ] 涉及外部写入的任务具备幂等性。
- [ ] 代码任务在 `OnStop` 中优雅停止。
- [ ] 修改组件或注册表后已执行生成和检查命令。
