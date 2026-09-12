# 权限管理

框架统一处理 JWT、Session、管理端菜单权限和请求身份注入。

## 配置

```yaml
cool:
  auth:
    jwt:
      currentKeyId: primary
      keys:
        primary: "replace-with-a-fixed-secret-of-at-least-32-bytes"
    session:
      type: redis
```

两份应用配置都使用 Redis Session，并直接配置 JWT 签名密钥。JWT 使用 HS256，签名密钥至少 32 字节；所有服务实例必须使用相同的固定密钥。默认有效期：

- Access Token：2 小时。
- Refresh Token：15 天。

Redis Session 使用默认连接组：

```yaml
cool:
  auth:
    session:
      type: redis
      group: default
      prefix: "cool:session:"

redis:
  default:
    address: "127.0.0.1:6379"
    db: 0
```

## 请求令牌

客户端通过 `Authorization` 请求头直接携带 Access Token：

```text
Authorization: <access-token>
```

当前实现接收原始令牌，不要添加 `Bearer ` 前缀。

## 管理端权限

`/admin/**` 路由会自动按路径推导权限标识：

```text
/admin/base/sys/user/page -> base:sys:user:page
```

后台菜单或按钮的 `perms` 必须包含相同标识。一个按钮需要多个权限时用逗号分隔：

```json
{
  "name": "查询",
  "type": 2,
  "perms": "shop:goods:list,shop:goods:page,shop:goods:info"
}
```

路径中包含 `comm` 的通用接口只校验登录，不校验菜单权限。`/admin/dict/info/data` 同样只校验登录。

`/app/**` 当前校验应用端登录，但不根据后台菜单权限标识授权。应用端的业务权限应在 Service 中显式判断。

## 公开接口

无需登录的路由添加：

```go
Tags: []gnctrl.URLTag{{Name: gnctrl.TagIgnoreToken}},
```

只对登录、注册、验证码等真正公开的接口使用该标签。

## 获取当前身份

管理端：

```go
identity, err := auth.Admin(ctx)
if err != nil {
   return err
}

userID := identity.UserID
username := identity.Username
roleIDs := identity.RoleIDs()
```

应用端：

```go
identity, err := auth.App(ctx)
if err != nil {
   return err
}

userID := identity.ID
```

身份只能从处理当前请求的 `ctx` 中读取。不要信任客户端提交的用户 ID 替代登录身份。

## 令牌续期和退出

登录接口返回 Access Token 与 Refresh Token。Access Token 过期后，客户端调用刷新接口并提交 Refresh Token；刷新成功会轮换令牌，旧 Refresh Token 不能再次使用。

退出登录会撤销当前 Session。用户密码、角色、状态等授权信息变化时，应通过现有权限服务更新，使旧 Session 按业务规则失效。

## 安全要求

- 启动前必须替换配置文件中的默认 JWT 密钥，且不能使用少于 32 字节的值。
- 生产环境使用独立的固定随机密钥，并限制配置文件访问权限。
- 多实例部署必须使用共享的 Redis Session。
- Refresh Token 只用于刷新接口，不能访问普通业务接口。
- 不在日志中记录 Token、密码或完整 Session 内容。
