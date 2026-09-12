# 数据库

## 配置

数据库使用 GoFrame ORM 配置。`manifest/config/config.yaml` 和 `manifest/config/config.local.yaml` 都包含默认连接组：

```yaml
database:
  default:
    - link: "mysql:root:123456@tcp(127.0.0.1:3306)/cool-go?loc=Local&parseTime=true&charset=utf8mb4"
      createdAt: createTime
      updatedAt: updateTime
```

直接运行 `go run ./cmd/cool run` 时读取 `config.yaml`；本地开发使用：

```bash
COOL_CONFIG_FILE=manifest/config/config.local.yaml go run ./cmd/cool run
```

部署前应替换默认数据库密码并限制配置文件访问权限，不要保留仓库中的示例凭据。

当前数据库版本要求：

- MySQL 8.x，不支持 MariaDB。
- PostgreSQL 9.5 及以上。
- SQLite 3.24 及以上。

项目入口已经注册 MySQL、PostgreSQL 和 SQLite 的 GoFrame 驱动，不需要额外安装依赖或修改入口文件。切换数据库时，将连接配置改为对应的 `mysql`、`pgsql` 或 `sqlite` 驱动格式即可；应用启动时会探测数据库产品、版本和事务能力，不满足上述基线会直接报错。

## 实体

业务实体放在 `modules/<模块>/entity`：

```go
package entity

import (
   "github.com/gogf/gf/v2/frame/g"
   "github.com/toothdy/cool-admin-go-next/cool-next/core/gnentity"
)

type Goods struct {
   g.Meta `orm:"table:shop_goods" description:"商品"`
   gnentity.Base
   Title  string   `json:"title" orm:"title" description:"标题" cool:"size=255"`
   Price  float64  `json:"price" orm:"price" description:"价格" cool:"precision=10,scale=2"`
   Status int32    `json:"status" orm:"status" description:"状态" cool:"default=1"`
   Remark *string  `json:"remark" orm:"remark" description:"备注" cool:"size=500"`
   Labels []string `json:"labels" orm:"labels" description:"标签" cool:"json=true"`
}

func GoodsSchema() gnentity.Schema {
   return gnentity.Schema{Indexes: []gnentity.Index{
      gnentity.IndexOf("idx_shop_goods_status", "status"),
      gnentity.UniqueIndexOf("uk_shop_goods_title", "title"),
   }}
}
```

每个实体都必须有同名 `Schema` 函数。`gnentity.Base` 提供：

- `id`
- `createTime`
- `updateTime`

## 字段规则

每个持久化字段必须有 `json`、`orm` 和 `description` 标签。常用 `cool` 约束：

| 约束 | 用途 |
| --- | --- |
| `size=255` | 字符串长度 |
| `default=0` | 默认值 |
| `precision=10,scale=2` | 小数精度 |
| `json=true` | JSON 数组或对象 |
| `transient` | 非数据库字段 |

指针字段表示数据库可空。需要输出但不持久化的字段使用 `transient`，且不能声明 `orm`：

```go
CategoryName *string `json:"categoryName" description:"分类名称" cool:"transient"`
```

## 索引与关联

索引统一在实体的 `Schema` 函数中声明。关联关系不使用数据库外键，由业务代码保证引用存在、删除顺序和事务一致性。

多表查询使用 ORM Join 或 Controller 查询 DSL。Controller 查询 DSL 示例见 [控制器](./controller.md)。

## 事务

自定义路由默认由框架开启事务。Service 中需要主动建立事务时使用数据库 Runtime：

```go
return runtime.Runner().Within(ctx, func(txCtx context.Context) error {
   model, err := service.Model(txCtx)
   if err != nil {
      return err
   }
   if _, err = model.Data(write).Update(); err != nil {
      return exception.WrapCore(err, "更新商品失败")
   }

   return nil
})
```

事务闭包中必须继续传递 `txCtx`，不能换回外层 `ctx`。嵌套调用 `Within` 会复用当前框架事务。

## 时间和删除

`createdAt`、`updatedAt` 在数据库配置中映射到项目的 `createTime`、`updateTime`，由 ORM 自动维护，不需要业务代码手动赋值。

项目的 CRUD 删除行为由 `cool.crud.softDelete` 控制，并与回收站能力配合。业务代码应调用 `Base.Delete`，不要自行写 `deletedAt` 条件模拟另一套软删除。

## 生成和同步

实体或索引变化后执行：

```bash
go run ./cmd/cool generate
go run ./cmd/cool check
```

生产数据库的结构变更应经过备份和评审，不要依赖启动过程替代正式迁移流程。
