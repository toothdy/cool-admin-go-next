# 缓存

框架使用 GoFrame `gcache`。单实例可使用内存缓存，多实例需要使用 Redis 缓存。

## 内存缓存

```go
import (
   "context"
   "time"

   "github.com/gogf/gf/v2/os/gcache"
)

cache := gcache.New()

if err := cache.Set(ctx, "shop:goods:1", goods, 10*time.Minute); err != nil {
   return err
}

value, err := cache.Get(ctx, "shop:goods:1")
if err != nil {
   return err
}
if !value.IsNil() {
   // 使用缓存值
}
```

内存缓存只在当前进程有效，服务重启后丢失，也不会在多个实例间同步。

## Redis 缓存

先配置 Redis：

```yaml
redis:
  default:
    address: "127.0.0.1:6379"
    db: 0
```

创建 Redis 适配器缓存：

```go
import (
   "github.com/gogf/gf/v2/database/gredis"
   "github.com/gogf/gf/v2/os/gcache"
)

adapter := gcache.NewAdapterRedis(gredis.Instance("default"))
cache := gcache.NewWithAdapter(adapter)
```

Redis 配置由框架启动过程注册，业务代码直接使用连接组名称。

## Service 中封装

缓存读取、回源和失效在 Service 中显式编写：

```go
func (service *GoodsService) LoadInfo(ctx context.Context, id uint64) (GoodsItem, error) {
   key := fmt.Sprintf("shop:goods:%d", id)
   cached, err := service.cache.Get(ctx, key)
   if err != nil {
      return GoodsItem{}, exception.WrapCore(err, "读取商品缓存失败")
   }
   if !cached.IsNil() {
      var result GoodsItem
      if err = cached.Scan(&result); err == nil {
         return result, nil
      }
   }

   result, err := service.loadFromDB(ctx, id)
   if err != nil {
      return GoodsItem{}, err
   }
   if err = service.cache.Set(ctx, key, result, 10*time.Minute); err != nil {
      return GoodsItem{}, exception.WrapCore(err, "写入商品缓存失败")
   }

   return result, nil
}
```

新增、更新或删除数据成功后，删除对应缓存键。缓存失效通常应在数据库事务提交后执行，避免其他请求缓存到未提交或已回滚的数据。

## 注意事项

- 缓存 Key 使用稳定的业务前缀，例如 `shop:goods:<id>`。
- 所有缓存都应设置合理的过期时间。
- Redis 适配器的 `Clear` 和 `Size` 可能作用于整个 Redis DB，业务代码不要随意调用。
- 多个系统共用 Redis 时，应使用独立 DB 或严格区分 Key 前缀。
- 缓存失败是否降级应由业务决定；鉴权、限流等安全状态不能静默降级。
