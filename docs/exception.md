# 异常处理

业务代码返回 `error`，框架统一转换为响应。不要在 Controller 中自行拼装错误 JSON。

## 错误类型

| 构造函数 | 业务码 | 用途 |
| --- | --- | --- |
| `exception.Comm` | 1001 | 可安全展示给用户的业务失败 |
| `exception.Validate` | 1002 | 参数或业务输入校验失败 |
| `exception.Core` | 1003 | 配置、依赖或内部状态错误 |

成功响应业务码为 `1000`。

## 使用

```go
if goods == nil {
   return exception.Comm("商品不存在")
}

if price <= 0 {
   return exception.Validate("价格必须大于零")
}

if service.database == nil {
   return exception.Core("数据库服务未初始化")
}
```

需要指定 HTTP 状态码时传入第二个参数：

```go
return exception.Comm("权限不足", http.StatusForbidden)
```

## 包装底层错误

数据库、文件、网络等底层错误使用包装函数保留原因和堆栈：

```go
if err := model.Scan(&rows); err != nil {
   return exception.WrapCore(err, "查询商品失败")
}
```

对应函数还有 `WrapComm` 和 `WrapValidate`。传入的原始错误为 `nil` 时，包装函数返回 `nil`。

核心异常的内部消息不会直接返回客户端，客户端只会收到安全的 `core fail`。需要给用户展示的预期业务提示应使用 `Comm` 或 `Validate`。

## 记录日志

```go
g.Log().Error(ctx, "处理商品失败", exception.LogText(err))
```

`exception.LogText` 会保留错误堆栈，并对 Token、密码、Authorization、Secret、DSN 等常见敏感内容脱敏。不要直接把 `err.Error()` 或连接配置输出到日志。

## 原则

- 可预期的业务失败使用 `Comm`。
- 客户端输入不合法使用 `Validate`。
- 基础设施和程序状态错误使用 `Core` 或 `WrapCore`。
- Service 返回错误，交给统一响应层处理。
