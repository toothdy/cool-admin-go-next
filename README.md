<p align="center">
  <a href="https://cool-js.com" target="_blank"><img src="https://cool-show.oss-cn-shanghai.aliyuncs.com/admin/logo.png" width="200" alt="Cool-Admin Logo" /></a>
</p>

<p align="center">
  Cool-Admin Go 版是基于 GoFrame v2 的后台权限管理系统，提供模块化、插件化、声明式 CRUD、权限认证和代码生成能力，帮助开发者快速构建后台管理服务。前往 <a href="https://goframe.org" target="_blank">GoFrame 官网</a> 了解更多。
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26.6-00ADD8?style=flat-square&logo=go" alt="Go version" />
  <img src="https://img.shields.io/badge/GoFrame-2.10.2-2E7D32?style=flat-square" alt="GoFrame version" />
</p>

## 特性

- **声明式 CRUD**：声明实体、服务和控制器后，即可生成新增、删除、修改、详情、列表和分页接口。
- **静态代码生成**：通过源码分析生成依赖装配、路由、实体描述和数据库写入对象，启动阶段无需运行时反射扫描。
- **模块化**：模块独立管理配置、实体、服务、控制器、中间件、定时任务和初始化数据。
- **插件化**：基于 WASI 与 wazero 的沙箱插件体系，支持构建和分发 `.cool` 插件包。
- **权限认证**：内置 JWT、会话、RBAC 权限、bcrypt 密码摘要、验证码和登录风控。
- **数据库适配**：统一支持 MySQL、PostgreSQL 和 SQLite，并根据实体声明同步安全的表结构变更。
- **软删除与回收站**：CRUD 数据可统一进入回收站，并支持恢复、还原和定时清理。
- **多传输协议**：HTTP 默认可用，按配置可启用 gRPC。
- **可靠消息**：内置 Outbox、Inbox 和消息重放工具，适合需要事务消息的业务场景。

## 技术栈

- 后端：**`Go` `GoFrame v2`**
- 前端：**`Vue.js` `Element Plus` `JSX` `Pinia` `Vue Router`**
- 数据库：**`MySQL 8.x` `PostgreSQL 9.5+` `SQLite 3.24+`**
- 缓存与会话：**`Redis`**（也可使用内存模式）
- 插件运行时：**`WASI` `wazero`**

## 相关链接

- 官网：[https://cool-js.com](https://cool-js.com)
- 在线演示：[https://show.cool-admin.com](https://show.cool-admin.com)
- 演示账号：`admin`
- 演示密码：`123456`
- 视频教程：[官方 B 站视频教程](https://www.bilibili.com/video/BV1j1421R7aB)

### 项目前端

- GitHub：[cool-admin-vue](https://github.com/cool-team-official/cool-admin-vue)
- Gitee：[cool-admin-vue](https://gitee.com/cool-team-official/cool-admin-vue)
- GitCode：[cool-admin-vue](https://gitcode.com/cool_team/cool-admin-vue)

<img src="https://cool-show.oss-cn-shanghai.aliyuncs.com/admin/home-mini.png" alt="Cool-Admin 后台首页" />

## 快速开始

### 环境要求

- Go `1.26.6`
- MySQL `8.x`，并提前创建数据库 `cool-go`
- Redis（本地配置默认使用 Redis 保存会话和风控状态）

### 配置

项目提供两份完整配置：

- `manifest/config/config.yaml`：默认配置，关闭 EPS、数据库种子和菜单种子导入。
- `manifest/config/config.local.yaml`：本地开发配置，开启 EPS、数据库种子和菜单种子导入。

两份配置都使用 Redis 保存 Session 和登录风控状态，并包含 MySQL、Redis 和 JWT 配置。首次启动前按实际环境修改 MySQL、Redis 连接：

```yaml
database:
  default:
    - link: "mysql:root:123456@tcp(127.0.0.1:3306)/cool-go?loc=Local&parseTime=true&charset=utf8mb4"
      createdAt: createTime
      updatedAt: updateTime

redis:
  default:
    address: "127.0.0.1:6379"
    db: 0
```

还需要把 `cool.auth.jwt.keys.primary` 改为至少 32 字节的固定随机密钥。仓库中的默认值仅用于标识配置项，长度不满足框架校验，不能直接用于启动或生产环境。

### 下载依赖并运行

```bash
go mod download
COOL_CONFIG_FILE=manifest/config/config.local.yaml go run ./cmd/cool run
```

服务默认监听 [http://localhost:8001](http://localhost:8001)。`config.local.yaml` 开启了 `initDB` 和 `initMenu`，首次启动会自动创建业务表并导入模块初始化数据。

不设置 `COOL_CONFIG_FILE` 时，框架默认读取 `manifest/config/config.yaml`：

```bash
go run ./cmd/cool run
```

`cool run` 会先检查生成代码是否与源码一致。如果提示 `modules/modules.go` 过期，执行：

```bash
go run ./cmd/cool generate
COOL_CONFIG_FILE=manifest/config/config.local.yaml go run ./cmd/cool run
```

## CRUD：快速增删改查

下面以 `demo` 模块的商品管理为例。框架会根据目录和函数名推导路由，无需手工注册模块、依赖或 HTTP Handler。

### 1. 声明模块

新建 `modules/demo/config.go`：

```go
package demo

import "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

// 示例模块运行配置
type Config struct{}

// 示例模块及其默认配置
func ModuleConfig() module.Declaration[Config] {
	return module.Declaration[Config]{
		Name:        "示例模块",
		Description: "演示快速 CRUD",
	}
}
```

### 2. 声明实体

新建 `modules/demo/entity/goods.go`：

```go
package entity

import (
	"github.com/gogf/gf/v2/frame/g"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

// 商品
type Goods struct {
	g.Meta `orm:"table:demo_goods" description:"商品"`
	gnentity.Base
	Title string  `json:"title" orm:"title" description:"标题" cool:"size=255"`
	Pic   string  `json:"pic" orm:"pic" description:"图片" cool:"size=255"`
	Price float64 `json:"price" orm:"price" description:"价格" cool:"precision=10,scale=2"`
}

// 商品表约束
func GoodsSchema() gnentity.Schema {
	return gnentity.Schema{}
}
```

`gnentity.Base` 已包含 `id`、`createTime` 和 `updateTime`。应用启动时会根据实体声明创建表；对已有表只自动执行受支持的安全变更，存在破坏性差异时会停止启动并报告原因。

### 3. 声明服务

新建 `modules/demo/service/goods.go`：

```go
package service

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/demo/entity"
)

// 商品业务服务
type GoodsService struct {
	*gnservice.Base[entity.Goods, uint64]
}

// 创建商品业务服务
func NewGoods(base *gnservice.Base[entity.Goods, uint64]) *GoodsService {
	return &GoodsService{Base: base}
}
```

嵌入的 `gnservice.Base` 已实现六个标准 CRUD 操作。需要自定义业务规则时，可在 `GoodsService` 上覆盖对应方法。

### 4. 声明控制器

新建 `modules/demo/controller/admin/goods.go`：

```go
package admin

import (
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnctrl"
	"github.com/toothdy/cool-admin-go-next/modules/demo/entity"
	"github.com/toothdy/cool-admin-go-next/modules/demo/service"
)

// 商品接口
func AdminDemoGoodsController(goods *service.GoodsService) gnctrl.Definition {
	return gnctrl.Admin().
		Options(gnctrl.RouterOptions{Description: "商品", TagName: "商品"}).
		Curd(gnctrl.CurdOption{
			API:     gnctrl.AllAPI(),
			Entity:  entity.Goods{},
			Service: goods,
		}).
		Build()
}
```

生成代码并启动：

```bash
go run ./cmd/cool generate
COOL_CONFIG_FILE=manifest/config/config.local.yaml go run ./cmd/cool run
```

此时会生成以下接口：

- `POST /admin/demo/goods/add`：新增
- `POST /admin/demo/goods/delete`：删除
- `POST /admin/demo/goods/update`：修改
- `GET /admin/demo/goods/info`：详情
- `POST /admin/demo/goods/list`：列表
- `POST /admin/demo/goods/page`：分页

> `modules/modules.go` 由 `cool generate` 生成，不要手工修改。

## 构建与检查

```bash
# 生成静态装配代码
go run ./cmd/cool generate

# 检查生成代码是否最新
go run ./cmd/cool check

# 检查源码并构建到 bin/cool-admin-go-next
go run ./cmd/cool build

# 执行格式、依赖、静态分析、架构和构建检查
make check
```

生产环境直接使用默认的 `manifest/config/config.yaml`。部署前必须替换其中的数据库连接、Redis 连接和 JWT 签名密钥，并按需调整日志配置；该配置默认关闭 EPS、数据库种子和菜单种子导入。
