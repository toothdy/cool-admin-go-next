# Go 插件开发

本文档说明如何为 `cool-admin-go-next` 开发 Go 插件。插件最终编译为 WASI WebAssembly 模块，并以 `.cool` 文件安装到 Cool Admin。插件运行在 wazero 沙箱中，只能通过 SDK 和宿主开放的 Host API 访问宿主能力。

## 1. 开发前准备

- Go 1.26 或更高版本。
- 一个可用的 `cool-admin-go-next` 源码目录，或已发布的 `cool-plugin` 工具。
- Cool Admin 宿主版本满足 `plugin.json` 中的 `runtime.minHostVersion`。

在仓库根目录编译 CLI：

```bash
cd cool-admin-go-next
go install ./cmd/cool-plugin
```

确认工具可用：

```bash
cool-plugin --help
```

如果直接从当前源码运行 `cool-plugin init`，脚手架会自动为 SDK 写入本地 `replace`；使用已发布的 CLI 时则使用对应的 SDK 版本。

## 2. 创建插件项目

`init` 只能写入空目录：

```bash
cool-plugin init --module example.com/acme/echo ../echo-plugin
cd ../echo-plugin
```

生成的项目结构如下；`assets/` 不是脚手架必建目录，需要分发额外资源时自行创建：

```text
echo-plugin/
├── go.mod
├── plugin.json
├── README.md
├── plugin/
│   └── plugin.go
├── cmd/
│   └── wasm/
│       └── main.go
└── assets/（可选）
```

各文件职责：

| 文件或目录 | 作用 |
| --- | --- |
| `go.mod` | 插件自己的 Go module，依赖 `github.com/toothdy/cool-admin-go-next` |
| `plugin/plugin.go` | 定义方法和生命周期，业务代码的主要位置 |
| `cmd/wasm/main.go` | WASI 入口和 ABI 桥接，通常无需修改 |
| `plugin.json` | 插件元数据、运行时要求和默认配置 |
| `README.md` | 安装后展示给管理员的使用说明 |
| `logo` 指向的文件 | 安装后展示的图标 |
| `assets/` | 需要随包分发的其他普通文件 |

不要删除 `cmd/wasm/main.go` 的 `wasip1` 构建标签和 ABI 导出。`cool-plugin build` 会固定构建 `./cmd/wasm`。

## 3. 编写插件代码

### 3.1 注册插件

插件定义只能注册一次。推荐在 `plugin` 包提供 `Plugin` 函数，再由 `cmd/wasm/main.go` 调用 `sdk.Register`：

```go
package plugin

import (
   "context"
   "errors"

   "github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk"
)

type Config struct {
   Prefix string `json:"prefix"`
}

type EchoRequest struct {
   Value string `json:"value"`
}

type EchoResponse struct {
   Value string `json:"value"`
}

func Plugin() sdk.Definition {
   return sdk.Define(
      sdk.Method("echo", echo),
      sdk.Ready(ready),
      sdk.Shutdown(shutdown),
   )
}

func ready(ctx context.Context) error {
   config, err := sdk.Config[Config](ctx)
   if err != nil {
      return err
   }
   if config.Prefix == "" {
      return errors.New("prefix 不能为空")
   }
   return nil
}

func echo(ctx context.Context, request EchoRequest) (EchoResponse, error) {
   config, err := sdk.Config[Config](ctx)
   if err != nil {
      return EchoResponse{}, err
   }
   return EchoResponse{Value: config.Prefix + request.Value}, nil
}

func shutdown(context.Context) error {
   return nil
}
```

然后在 `cmd/wasm/main.go` 中注册：

```go
func init() {
   sdk.Register(plugin.Plugin())
}
```

`Ready` 在实例初始化时执行；返回错误会使实例初始化失败。`Shutdown` 在实例关闭前执行；应停止后台工作并释放资源。只实现实际需要的生命周期回调即可。

### 3.2 方法规则

- 方法名必须以小写字母开头，只能包含 ASCII 字母和数字，长度 1 至 64，例如 `echo`、`readData`。
- `sdk.Method` 自动完成 JSON 请求解码和响应编码，适合绝大多数方法。
- `sdk.RawMethod` 接收和返回 `json.RawMessage`，适合需要自行处理 JSON 的方法。
- `sdk.Method` 使用标准 `json.Unmarshal`：非法 JSON 和字段类型错误会失败，但未知字段会被忽略。需要拒绝未知字段时，使用 `sdk.RawMethod` 配合 `json.Decoder.DisallowUnknownFields()`。
- SDK 不会执行 Controller DTO 的 `v` 标签校验，插件方法需要自行校验必填字段和业务约束。
- 请求或响应必须是合法 JSON；处理失败直接返回 `error`。
- 未捕获的 panic 会转换为插件错误，但不应依赖 panic 进行业务控制。

原始 JSON 方法示例：

```go
func rawEcho(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
   return append(json.RawMessage(nil), input...), nil
}

func Plugin() sdk.Definition {
   return sdk.Define(sdk.RawMethod("rawEcho", rawEcho))
}
```

### 3.3 读取配置和调用上下文

管理员启用插件时，宿主会把配置 JSON 传入每个实例。使用泛型 `sdk.Config[T]` 解码当前配置：

```go
config, err := sdk.Config[Config](ctx)
```

`sdk.InvocationID(ctx)` 返回 `(int64, bool)`；第二个返回值表示当前 Context 是否包含插件调用信息，调用 ID 可用于日志关联。插件不能从普通 `context.Context` 推断登录用户；需要身份或链路信息时，应调用 `context.get` Host API。

## 4. Manifest：plugin.json

`plugin.json` 使用严格 JSON 解析，未知字段、尾随的多余 JSON 值和非法路径都会被拒绝。最小示例：

```json
{
  "schemaVersion": 1,
  "name": "Echo 插件",
  "key": "echo-plugin",
  "singleton": true,
  "version": "1.0.0",
  "description": "返回带前缀的文本",
  "author": "ACME",
  "readme": "README.md",
  "runtime": {
    "abi": "cool.plugin/v1",
    "module": "plugin.wasm",
    "minHostVersion": "2.0.0"
  },
  "config": {
    "prefix": "echo: "
  }
}
```

字段说明：

| 字段 | 要求 |
| --- | --- |
| `schemaVersion` | 当前必须为 `1` |
| `name` | 展示名称，必填，最多 100 个字符 |
| `key` | 全局唯一标识，使用小写字母、数字和连字符，1 至 64 个字符，不能为 `plugin` |
| `hook` | 可选业务挂载点，同样使用小写字母、数字和连字符 |
| `singleton` | `true` 表示所有调用共享一个实例；否则宿主可创建多个实例 |
| `version` | SemVer，例如 `1.2.0` 或 `1.2.0-beta.1` |
| `description` | 可选描述，最多 500 个字符 |
| `author` | 必填，最多 100 个字符 |
| `logo` | 可选包内相对路径，必须指向普通文件 |
| `readme` | 可选包内相对路径，必须指向普通文件 |
| `runtime.abi` | 当前必须为 `cool.plugin/v1` |
| `runtime.module` | 当前必须为 `plugin.wasm` |
| `runtime.minHostVersion` | SemVer，宿主版本低于该值时拒绝安装或加载 |
| `config` | 必填 JSON object，值为管理员配置的默认值 |

路径必须使用 `/`，不能是绝对路径、包含 `..`、反斜杠或 `:`。`logo`、`readme` 引用的文件以及 `assets/` 下的普通文件会被打入 `.cool` 包。

`config` 只描述默认值，不是 Go 类型定义。插件代码中的配置结构应使用对应的 `json` 标签，并兼容管理员修改后的值。

## 5. 宿主能力（Host API）

在插件中统一使用：

```go
result, err := sdk.HostCall(ctx, "operation.name", input)
```

`input` 必须是合法 JSON；宿主响应也是 JSON。当前内建操作如下：

| 操作 | 请求示例 | 响应或用途 |
| --- | --- | --- |
| `config.get` | `{}` | 读取当前插件配置 |
| `context.get` | `{}` | 获取 trace ID、调用来源和已认证身份摘要 |
| `log.write` | `{"level":"info","message":"同步完成","fields":{"count":1}}` | 写入结构化日志，级别为 `debug/info/warn/error` |
| `cache.get` | `{"key":"token"}` | 返回 `{found,value}`；缓存 key 按插件隔离 |
| `cache.set` | `{"key":"token","value":{"id":1},"ttlMs":60000}` | 写入 JSON 缓存，`ttlMs` 可省略 |
| `cache.delete` | `{"key":"token"}` | 返回 `{deleted}` |
| `file.read` | `{"path":"state.json"}` | 读取插件专属数据目录中的文件，返回 `{data,size}` |
| `file.write` | `{"path":"state.json","data":"aGVsbG8="}` | 写入插件专属数据目录，返回 `{size}` |
| `file.delete` | `{"path":"state.json"}` | 删除文件或空目录，返回 `{deleted}` |
| `file.list` | `{"path":"."}` | 列出目录，返回 `{entries}` |
| `http.do` | `{"method":"GET","url":"https://example.com"}` | 发起受宿主限制的 HTTP 请求 |
| `plugin.call` | `{"target":"other-plugin","method":"echo","input":{"value":"x"}}` | 调用另一个已启用插件 |
| `host.call` | `{"operation":"custom.name","input":{}}` | 调用宿主额外注册的适配器（是否存在取决于宿主配置） |

内建 Host API 会严格解析请求对象，未知字段和尾随 JSON 会被拒绝。文件 API 的路径始终相对于当前插件自己的数据目录，不能访问宿主任意路径；`file.read`、`file.write` 的 `data` 以及 `http.do` 的 `body` 都是 Go `[]byte`，在 JSON 中使用 Base64 字符串。`http.do` 只允许 `http` 和 `https`，请求、响应和重定向次数受宿主配置限制。插件间调用会检查调用环路和最大深度。

Host API 失败时返回稳定错误码，例如 `PLUGIN_INVALID_INPUT`、`PLUGIN_TIMEOUT`、`PLUGIN_RESOURCE_EXHAUSTED` 和 `PLUGIN_HOST_CALL_FAILED`。插件应将这些错误向上返回，避免吞掉原因。

## 6. 检查、测试、构建和打包

所有命令都可以在插件目录执行，也可以把目录作为最后一个参数传入：

```bash
# 校验 go.mod、plugin.json、资源文件、Go 版本
cool-plugin check

# 执行 go test ./...
cool-plugin test

# 测试后构建 plugin.wasm
cool-plugin build

# 构建并生成 <plugin.json 的 name>.cool
cool-plugin pack
```

`build` 的实际构建参数等价于：

```bash
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 \
  go build -buildmode=c-shared -trimpath -buildvcs=false \
  -o plugin.wasm ./cmd/wasm
```

CLI 会在构建后加载并校验 WASM 的 ABI、必需导出和 Host API 导入；校验失败不会留下有效产物。`pack` 会把以下内容打入 ZIP 格式的 `.cool` 文件：

- `plugin.json`
- `plugin.wasm`
- `logo` 和 `readme` 引用的文件
- `assets/` 下的文件

当前默认限制为：压缩包不超过 32 MB，解压后不超过 64 MB，最多 256 个条目，单次插件载荷不超过 4 MB。宿主可以进一步调整这些限制。

## 7. 常见问题

### `插件构建需要 Go 1.26 或更高版本`

CLI 会检查本机 `go version`。升级 Go 后重新执行 `cool-plugin check`。

### `缺少 cmd/wasm` 或 `缺少 plugin.wasm`

命令必须在插件项目目录执行，且项目必须保留脚手架生成的 `cmd/wasm/main.go`。不要使用普通 `go build` 生成插件，使用 `cool-plugin build`。

### `plugin.json` 校验失败

检查必填字段、SemVer 格式、`runtime.abi`、`runtime.module` 和相对路径。JSON 中不能出现未定义字段；`key`、`hook` 只能使用小写字母、数字和连字符。

### 方法调用返回 `PLUGIN_INVALID_INPUT`

检查调用方发送的内容是否为合法 JSON，以及字段类型是否符合请求结构。`sdk.Method` 会忽略未知字段，因此必填字段和业务值仍需在处理函数中显式校验；内建 Host API 则会拒绝未知字段。

### 返回 `PLUGIN_TIMEOUT` 或 `PLUGIN_RESOURCE_EXHAUSTED`

减少单次请求和响应大小，避免无限循环或长时间网络请求。HTTP、文件、WASM 内存和调用超时都由宿主统一限制，插件无法绕过这些限制。

### `Host API 操作未注册`

只能调用本文列出的内建操作，或由宿主明确注册的 `host.call` 适配器。不要把宿主内部 Go 函数名当作可调用操作名。

## 8. 发布前检查清单

- [ ] `plugin.json.key` 稳定且唯一，版本符合 SemVer。
- [ ] `runtime.abi` 为 `cool.plugin/v1`，`runtime.module` 为 `plugin.wasm`。
- [ ] `config` 默认值与 Go 配置结构一致。
- [ ] 方法名符合小写开头、最多 64 个字符的规则。
- [ ] `Ready`、`Shutdown` 正确返回错误，不遗留无法停止的后台任务。
- [ ] 通过 `cool-plugin check`、`cool-plugin test`、`cool-plugin pack`。
- [ ] README 说明配置项、Host API 依赖、权限边界和故障处理方式。
