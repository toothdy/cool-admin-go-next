# 模块开发

## 目录结构

所有模块位于 `modules` 目录，生成器会在该目录生成 `modules.go`：

```text
modules/
  ├── config.go              # 必需
  ├── db.json                # 可选
  ├── menu.json              # 可选
  ├── controller/            # 必需
  │   ├── admin/
  │   └── app/
  ├── dto/                   # 可选
  ├── entity/                # 必需
  ├── middleware/            # 可选
  ├── schedule/              # 可选
  └── service/               # 必需
```

`modules/<模块名>` 是单个模块的根目录。除 `config.go` 外，业务 Go 文件必须放入上面的协议目录；测试文件不受此限制。协议目录可以继续划分子目录，例如 `service/upload`。

新增模块后执行 `go run ./cmd/cool generate`，不要手动修改 `modules/modules.go`。

## 模块配置

每个模块必须在 `config.go` 中精确声明 `ModuleConfig`：

```go
package shop

import "github.com/toothdy/cool-admin-go-next/cool-next/core/module"

type Config struct {
   Enabled bool `json:"enabled"`
}

func ModuleConfig() module.Declaration[Config] {
   return module.Declaration[Config]{
      Name:        "商城",
      Description: "商品与订单管理",
      Order:       10,
      Defaults: Config{
         Enabled: true,
      },
   }
}
```

配置结构使用 `json` 标签。当前模块配置只取 `ModuleConfig()` 中的 `Defaults`，框架会复制该值并注入需要 `Config` 的构造函数，具体写法见下一节。

应用配置文件当前不支持 `modules` 节点，不能在 `manifest/config/*.yaml` 中覆盖模块配置；写入该节点会导致应用启动失败。需要调整模块默认值时修改 `Defaults` 后重新构建应用，需要由管理员动态维护的业务配置应存入业务表。

## 组件构造

组件使用普通 Go 构造函数，依赖通过参数声明：

```go
type GoodsService struct {
   *gnservice.Base[entity.Goods, uint64]
   enabled bool
}

func NewGoods(
   base *gnservice.Base[entity.Goods, uint64],
   config shop.Config,
) *GoodsService {
   return &GoodsService{Base: base, enabled: config.Enabled}
}
```

构造函数保持单一返回组件，初始化可能失败时返回 `(组件, error)`。不要在包级变量中自行维护 Service 单例。

## 生命周期

需要启动或停止后台资源的组件可实现生命周期接口：

```go
func (service *Worker) OnInit(ctx context.Context) error
func (service *Worker) OnStart(ctx context.Context) error
func (service *Worker) OnStop(ctx context.Context) error
func (service *Worker) Terminated() <-chan error
```

仅实现实际需要的方法。`OnStop` 应停止接收新任务并等待正在执行的工作结束。

## 初始化数据

### db.json

`db.json` 按表名组织普通记录：

```json
{
  "shop_category": [
    {
      "id": 1,
      "name": "默认分类"
    }
  ]
}
```

当前格式不支持 `@childDatas` 或 `@id` 引用。需要关联数据时直接填写稳定主键。

### menu.json

菜单使用 `childMenus` 表示层级，每个节点都应设置稳定且唯一的 `seedKey`：

```json
[
  {
    "name": "商品管理",
    "router": "/shop/goods",
    "type": 1,
    "viewPath": "modules/shop/views/goods/index.vue",
    "childMenus": [
      {
        "name": "查询",
        "perms": "shop:goods:page,shop:goods:info",
        "type": 2,
        "childMenus": [],
        "seedKey": "shop.goods.query"
      }
    ],
    "seedKey": "shop.goods"
  }
]
```

在配置中开启初始化：

```yaml
cool:
  initDB: true
  initMenu: true
```

导入状态保存在数据库表 `cool_seed_lock`，不是本地锁文件。生产环境完成初始化后应关闭自动导入。

## 生成与检查

新增或修改以下内容后需要重新生成：模块、实体、Service 构造函数、Controller、生命周期组件、种子文件。

```bash
go run ./cmd/cool generate
go run ./cmd/cool check
```

不要手动修改 `modules/modules.go`。
