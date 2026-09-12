# cool-admin-go-next 开发文档

本文档说明当前框架的模块、业务组件和 Go 插件开发方式。示例默认在项目根目录执行，适用于仓库当前版本。

## 文档目录

| 文档 | 内容 |
| --- | --- |
| [模块开发](./module.md) | 模块目录、配置、代码生成、初始化数据 |
| [插件开发](./plugin.md) | Go 插件脚手架、SDK、Manifest、Host API 和打包 |
| [控制器](./controller.md) | 路由、CRUD、参数绑定、查询配置 |
| [服务](./service.md) | Base Service、CRUD 重写、数据库访问 |
| [数据库](./db.md) | 数据库配置、实体、索引、事务 |
| [权限](./authority.md) | 登录、路由权限、身份信息、令牌续期 |
| [异常](./exception.md) | 业务异常、参数异常、核心异常和日志 |
| [缓存](./cache.md) | 内存缓存和 Redis 缓存 |
| [事件](./event.md) | 同步调用和可靠异步消息 |
| [任务](./task.md) | 代码定时任务和后台任务 |

开发普通业务功能时，从模块、Controller、Service 和数据库文档开始；开发独立分发的 WASM 插件时，直接阅读插件开发文档。权限、缓存、事件和任务按业务需要选读。

## 开发流程

1. 在 `modules/<模块名>` 下创建模块。
2. 编写 `config.go` 中的 `ModuleConfig()`、实体、Service 和 Controller。
3. 生成模块装配代码：

   ```bash
   go run ./cmd/cool generate
   ```

4. 检查项目：

   ```bash
   go run ./cmd/cool check
   ```

5. 配置数据库、Redis 和 JWT 密钥后，使用本地配置启动：

   ```bash
   COOL_CONFIG_FILE=manifest/config/config.local.yaml go run ./cmd/cool run
   ```

   不设置 `COOL_CONFIG_FILE` 时默认读取 `manifest/config/config.yaml`。

## 生成文件

`modules/modules.go` 由框架生成，包含模块装配、路由、实体描述和种子数据。不要手动修改该文件；修改模块声明后重新执行 `go run ./cmd/cool generate`。

## 基本约定

- 业务逻辑写在 `service`，Controller 只负责声明路由和绑定处理函数。
- 数据库写入使用 `gnservice.Mutable`、生成的 DO 或明确命名的写入结构体，不使用 `g.Map`。
- 错误使用 `exception` 包统一返回，底层错误使用 `exception.WrapCore` 保留原因。
- 查询接口显式配置 `gnctrl.NonTransactional()`；需要多步原子写入时使用框架事务。
