# 服务

Service 负责业务规则、数据库操作和跨组件协作。Controller 只声明路由并把请求交给 Service。

## 基础 Service

实体 Service 通常嵌入泛型 `gnservice.Base`：

```go
type GoodsService struct {
   *gnservice.Base[entity.Goods, uint64]
}

func NewGoods(base *gnservice.Base[entity.Goods, uint64]) *GoodsService {
   return &GoodsService{Base: base}
}
```

`Base` 提供 `Add`、`Delete`、`Update`、`Info`、`List` 和 `Page`。

## 重写 CRUD

使用完全相同的方法签名覆盖默认实现，并在需要时调用 `Base`：

```go
func (service *GoodsService) Add(
   ctx context.Context,
   input gnservice.AddInput[entity.Goods],
) (gnservice.AddResult[uint64], error) {
   value := input.One()
   if value == nil {
      return gnservice.AddResult[uint64]{}, exception.Validate("商品新增只支持单条记录")
   }
   if err := value.Set("status", 1); err != nil {
      return gnservice.AddResult[uint64]{}, err
   }

   return service.Base.Add(ctx, input)
}
```

更新时只处理明确提交的字段：

```go
func (service *GoodsService) Update(
   ctx context.Context,
   input gnservice.UpdateInput[entity.Goods, uint64],
) error {
   item := input.One()
   if input.IsMany() || item.Mutable() == nil {
      return exception.Validate("商品更新只支持单条记录")
   }
   value := item.Mutable()
   if value.Has("price") {
      price, _ := value.Get("price")
      // 校验 price
   }

   return service.Base.Update(ctx, input)
}
```

可用方法：

- `Mutable.Has(field)`：字段是否由客户端提交。
- `Mutable.Get(field)`：读取字段值。
- `Mutable.IsNull(field)`：是否显式提交 `null`。
- `Mutable.Set(field, value)`：服务端设置字段。
- `Mutable.SetNull(field)`：服务端设置为 `NULL`。
- `Mutable.Unset(field)`：移除本次写入字段。

需要前后置逻辑时，直接重写对应 CRUD 方法。

## ORM 查询

通过 `Model(ctx)` 获取绑定当前框架事务的实体 Model：

```go
func (service *GoodsService) Enabled(ctx context.Context) ([]GoodsRow, error) {
   model, err := service.Model(ctx)
   if err != nil {
      return nil, err
   }

   var rows []GoodsRow
   if err = model.
      Fields("id", "title", "price").
      Where("status", 1).
      OrderDesc("createTime").
      Scan(&rows); err != nil {
      return nil, exception.WrapCore(err, "查询可用商品失败")
   }

   return rows, nil
}
```

不要绕过 `Model(ctx)` 重新获取数据库连接，否则可能脱离当前事务。

## 数据库写入

常规 CRUD 使用 `gnservice.Mutable`。自定义 ORM 写入使用生成的 DO，或定义明确命名并带 `orm:"do:true"` 的写入结构体：

```go
type goodsWrite struct {
   g.Meta `orm:"do:true"`
   Status any `orm:"status"`
   Remark any `orm:"remark"`
}

_, err = model.
   Where("id", id).
   Data(goodsWrite{Status: 1}).
   Update()
```

未设置的 `any` 字段保持 `nil`，ORM 会忽略它。不要使用 `g.Map` 或 `map[string]any` 组织数据库写入。

## 原生只读 SQL

必须先通过 `gnservice.NativeSQL` 校验。它只接受单条 `SELECT` 或包含 `SELECT` 的 CTE：

```go
type goodsRow struct {
   ID    uint64 `orm:"id"`
   Title string `orm:"title"`
}

statement, err := gnservice.NativeSQL(
   "SELECT id, title FROM shop_goods WHERE status = ?",
   1,
)
if err != nil {
   return nil, err
}

var rows []goodsRow
if err = service.NativeQuery(ctx, statement, &rows); err != nil {
   return nil, err
}
```

原生 SQL 中仍需使用占位符，不拼接用户输入。写操作使用 ORM 和框架事务，不通过 `NativeSQL` 执行。

原生分页使用：

```go
pagination, err := service.SQLRenderPage(ctx, statement, query, &rows)
```

返回值可组合成：

```go
type GoodsPageResult struct {
   List       []goodsRow             `json:"list"`
   Pagination gnservice.Pagination   `json:"pagination"`
}
```
