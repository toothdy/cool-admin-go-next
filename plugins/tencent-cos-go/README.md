# 腾讯云 COS 上传插件

适用于 cool-admin-go-next 的腾讯云 COS 上传插件。插件使用腾讯官方 Go SDK，并兼容现有 cool-admin-vue 上传流程。

## 配置

```json
{
  "accessKeyId": "腾讯云 SecretId",
  "accessKeySecret": "腾讯云 SecretKey",
  "bucket": "存储桶名称，例如 example-1250000000",
  "region": "ap-guangzhou",
  "publicDomain": "https://example-1250000000.cos.ap-guangzhou.myqcloud.com",
  "durationSeconds": 1800,
  "allowPrefix": "/*"
}
```

`allowPrefix` 支持 `/*`、`*` 或 `app/base/*` 等目录限制。前端通过 `/admin/base/comm/upload` 获取 STS 临时凭证后直接上传 COS，长期密钥不会返回给前端。

`publicDomain` 同时用作前端 PostObject 上传地址和文件访问地址，必须配置为允许 PostObject 的 COS 存储桶域名，不能使用仅支持读取的 CDN 域名。

## 方法

- `mode`、`getMode`：返回 `{ "mode": "cloud", "type": "cos" }`。
- `upload`：空请求返回前端直传凭证；带宿主文件请求时完成服务端上传。
- `credentials`：返回前端直传凭证。
- `downAndUpload`：下载 HTTP 文件或读取插件 `/data` 文件后上传。
- `uploadWithKey`：将插件 `/data` 文件上传到指定 COS Key。
- `getConfig`：返回已脱敏的当前配置。

`getMetaFileObj` 无法跨 WASM JSON ABI 返回 Go SDK 对象，调用时会返回明确错误。

`downAndUpload` 的 HTTP 下载响应大小受宿主 `plugin.maxPayloadBytes` 限制（默认 4 MiB）。服务端上传的总时间受宿主插件调用超时限制。
