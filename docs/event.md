# 事件与可靠消息

根据一致性要求选择同步调用或 Outbox 可靠消息。

## 同步业务调用

需要立即得到结果、失败时回滚当前请求的逻辑，直接通过构造函数注入另一个 Service：

```go
type OrderService struct {
   inventory *InventoryService
}

func NewOrder(inventory *InventoryService) *OrderService {
   return &OrderService{inventory: inventory}
}

func (service *OrderService) Create(ctx context.Context, request CreateRequest) error {
   if err := service.inventory.Deduct(ctx, request.GoodsID, request.Quantity); err != nil {
      return err
   }

   return nil
}
```

这种方式调用关系明确，并会继续使用同一个 `ctx` 和框架事务。

## 可靠异步消息

业务数据提交成功后必须可靠投递的消息使用 `outbox.Enqueuer`。Outbox 只提供可靠落库、重试和消费事务协议，不内置具体消息中间件：

- 只要业务组件依赖 `outbox.Enqueuer`，项目中就必须有且只能有一个实现 `outbox.Publisher` 的发布组件。
- 只要声明了 Consumer，项目中就必须有且只能有一个 `outbox.ConsumerAdapter`。
- 当前仓库没有提供具体 Broker 实现，需要先由项目接入层适配 RabbitMQ、Kafka 或实际使用的消息系统；缺少适配组件时 `go run ./cmd/cool check` 会直接报错。

完成 Broker 适配后，在配置中开启：

```yaml
cool:
  outbox:
    enabled: true
```

定义稳定的消息 DTO：

```go
type OrderPaid struct {
   OrderID uint64 `json:"orderId"`
   UserID  uint64 `json:"userId"`
}
```

在需要发消息的 Service 构造函数中声明 `outbox.Enqueuer` 依赖：

```go
type OrderService struct {
   enqueuer outbox.Enqueuer
}

func NewOrder(enqueuer outbox.Enqueuer) *OrderService {
   return &OrderService{enqueuer: enqueuer}
}
```

创建并入队消息：

```go
message, err := outbox.New(
   "order-events",
   "order.paid",
   OrderPaid{OrderID: orderID, UserID: userID},
   outbox.WithKey(strconv.FormatUint(orderID, 10)),
)
if err != nil {
   return err
}
if err = service.enqueuer.Enqueue(ctx, message); err != nil {
   return exception.WrapCore(err, "发送订单支付消息失败")
}
```

`Enqueue` 会在框架事务中写入 Outbox。调用时继续传递当前业务 `ctx`，使业务写入和消息入队处于同一事务边界。

消息默认版本为 `1`。消息结构发生不兼容变化时，使用 `outbox.WithVersion(...)` 发布新版本，并让消费者在 `supportedVersions` 中明确列出可处理的版本。

## 消费者

消费者代码放在 `modules/<模块>/consumer`。使用稳定且唯一的消费者名称，并明确 Topic、消息类型和支持版本：

```go
func NewOrderPaidConsumer(service *OrderService) (outbox.ConsumerDefinition, error) {
   return outbox.Consume[OrderPaid](
      "shop.order-paid",
      "order-events",
      "order.paid",
      []uint32{1},
      func(ctx context.Context, message outbox.Incoming[OrderPaid]) error {
         return service.AfterPaid(ctx, message.Payload())
      },
   )
}
```

消费者可能收到重复投递，处理函数必须幂等。可以使用业务唯一键、状态机条件更新或唯一索引防止重复执行。

## 选择原则

- 同一请求内必须立即成功：直接调用 Service。
- 需要跨事务可靠异步处理：Outbox。
- 仅用于日志且允许丢失：直接记录日志。
- 不要使用 goroutine 代替可靠消息；进程退出时未完成任务会丢失。

开启 Outbox 后，只要项目中存在 Producer 或 Consumer，就不能再将 `cool.outbox.enabled` 设为 `false`。发布端与消费端是否都需要接入，取决于当前项目实际声明了哪类组件。
