# 控制器

Controller 使用 `gnctrl` 声明管理端或应用端路由。业务处理函数放在 Service 或 Handler 中。

## 自动路由

文件路径决定默认路由：

```text
modules/dict/controller/admin/info.go -> /admin/dict/info
modules/dict/controller/app/info.go   -> /app/dict/info
```

管理端使用 `gnctrl.Admin()`，应用端使用 `gnctrl.App()`：

```go
func AdminGoodsController(goods *service.GoodsService) gnctrl.Definition {
   return gnctrl.Admin().
      Options(gnctrl.RouterOptions{
         Description: "商品管理",
         TagName:     "商品管理",
      }).
      Build()
}
```

需要覆盖自动路径时，可向 `Admin` 或 `App` 传入不带开头 `/` 的相对路径。例如 `gnctrl.Admin("shop/catalog")` 对应 `/admin/shop/catalog`。

## CRUD

```go
func AdminGoodsController(goods *service.GoodsService) gnctrl.Definition {
   return gnctrl.Admin().
      Options(gnctrl.RouterOptions{
         Description: "商品管理",
         TagName:     "商品管理",
      }).
      Curd(gnctrl.CurdOption{
         API:     gnctrl.AllAPI(),
         Entity:  entity.Goods{},
         Service: goods,
      }).
      Build()
}
```

`gnctrl.AllAPI()` 注册以下接口：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/add` | 新增 |
| POST | `/delete` | 删除 |
| POST | `/update` | 更新 |
| GET | `/info` | 详情 |
| POST | `/list` | 列表 |
| POST | `/page` | 分页 |

只开放部分接口时使用：

```go
API: gnctrl.API(gnctrl.Info, gnctrl.List, gnctrl.Page),
```

常用 `CurdOption`：

```go
gnctrl.CurdOption{
   API:            gnctrl.AllAPI(),
   Entity:         entity.Goods{},
   Service:        goods,
   HiddenFields:   []gnctrl.ColumnRef{gnctrl.Field("internalCode")},
   ReadonlyFields: []gnctrl.ColumnRef{gnctrl.Field("status")},
   SortFields:     []gnctrl.ColumnRef{gnctrl.Field("price")},
   DefaultSort:    gnctrl.Field("createTime"),
   DefaultOrder:   gnctrl.Descending,
}
```

`Prefix` 会单独覆盖 CRUD 的基础路径，并不是在 Controller 路径后追加内容。它同样必须是不带开头 `/` 的相对路径，例如 `Prefix: "shop/manage"` 对应 `/admin/shop/manage`。自定义路由仍使用 Controller 自己的路径，通常不需要设置 `Prefix`。

## 查询配置

列表和分页可以分别配置查询规则：

```go
Curd(gnctrl.CurdOption{
   API:     gnctrl.AllAPI(),
   Entity:  entity.Goods{},
   Service: goods,
   PageQueryOp: gnctrl.StaticQuery(gnctrl.QueryOp{
      KeyWordLikeFields: []gnctrl.ColumnRef{
         gnctrl.Field("title"),
         gnctrl.Field("remark"),
      },
      FieldEq: []gnctrl.FieldEq{
         gnctrl.Eq(gnctrl.Field("status")),
         gnctrl.EqFrom(gnctrl.Field("categoryId"), "category"),
      },
      FieldLike: []gnctrl.FieldLike{
         gnctrl.Like(gnctrl.Field("title")),
      },
      AddOrderBy: []gnctrl.Order{
         gnctrl.Desc(gnctrl.Field("createTime")),
      },
   }),
})
```

- `KeyWordLikeFields`：使用请求中的关键字对多个字段模糊匹配。
- `FieldEq`：请求字段存在时追加等值条件。
- `FieldLike`：请求字段存在时追加模糊条件。
- `AddOrderBy`：追加固定排序。
- `StaticQuery`：返回结构固定，优先使用。
- `DynamicQuery`：需要根据当前身份等运行时信息生成完整查询配置时使用。

固定条件使用参数化表达式：

```go
Where: gnctrl.Where(
   gnctrl.EqValue(gnctrl.Field("status"), 1),
),
```

原始条件也必须传参数，不能拼接用户输入：

```go
Where: gnctrl.Where(
   gnctrl.RawWhere("a.price >= ?", 100),
),
```

关联查询示例：

```go
Join: []gnctrl.JoinOp{
   gnctrl.LeftJoin(
      entity.Category{},
      "category",
      gnctrl.On(
         gnctrl.FieldOf[entity.Goods]("categoryId"),
         gnctrl.FieldOf[entity.Category]("id"),
      ),
   ),
},
```

## 自定义接口

```go
func AdminGoodsController(goods *service.GoodsService) gnctrl.Definition {
   return gnctrl.Admin().
      Options(gnctrl.RouterOptions{
         Description: "商品管理",
         TagName:     "商品管理",
      }).
      Route(gnctrl.Route{
         Method:      http.MethodGet,
         Path:        "/enabled",
         Summary:     "可用商品",
         Handler:     gnctrl.Handle(goods.Enabled),
         Bind:        gnctrl.BindQuery,
         Transaction: gnctrl.NonTransactional(),
      }).
      Build()
}
```

处理函数使用 `context.Context`，可选一个指向具名结构体的 DTO 参数，并返回 `error` 或 `(结果, error)`：

```go
func (service *GoodsService) Enabled(
   ctx context.Context,
   request *dto.EnabledRequest,
) ([]dto.GoodsItem, error) {
   // 查询并返回结果
}
```

DTO 使用 `json` 标签声明字段名，使用 GoFrame 的 `v` 标签校验。绑定成功后框架会自动执行校验：

```go
type EnabledRequest struct {
   CategoryID uint64 `json:"categoryId" v:"required|min:1"`
   Keyword    string `json:"keyword"`
}
```

绑定方式包括 `BindJSON`、`BindQuery`、`BindForm`、`BindPath` 和 `BindFile`。GET、DELETE 和 HEAD 默认使用 `BindQuery`，其他方法默认使用 `BindJSON`；存在歧义或需要表单、路径参数、文件时应显式设置 `Bind`。请求字段类型错误、未知 JSON 字段或 `v` 校验失败都会返回参数异常。

自定义路由默认进入框架事务。纯查询和不需要事务的接口应显式设置：

```go
Transaction: gnctrl.NonTransactional(),
```

## 公开接口

无需登录的接口添加 `ignoreToken` 标签：

```go
Tags: []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
```

公开接口不会注入登录身份，不要在其中调用 `auth.Admin(ctx)` 或 `auth.App(ctx)`。

默认 CRUD 只公开指定接口时，在 `CurdOption` 中配置 `URLTag`：

```go
URLTag: &gnctrl.URLTag{
   Name: gnctrl.TagIgnoreToken,
   URL:  gnctrl.API(gnctrl.Info, gnctrl.List),
},
```

省略 `URL` 表示给当前启用的全部 CRUD 接口添加该标签。公开写接口风险较高，应优先使用自定义路由显式校验输入和业务约束。

## 修改后生成

Controller 是静态分析和生成的一部分。修改声明或处理函数签名后执行：

```bash
go run ./cmd/cool generate
go run ./cmd/cool check
```
