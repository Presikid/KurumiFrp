# 事件通知

通知默认关闭。管理员可选择一种消息格式，填写一个完整的公网 HTTPS 地址，并选择实例状态和配置变更事件。通知内容只包含事件、时间、实例 ID / 类型或配置版本，不包含配置、日志或令牌。通知地址本身可能含令牌，目标服务及其代理能看到它；通知结果只表示目标是否接受请求，不能保证最终送达。

通用 JSON、Discord、Slack 和 ntfy 使用原有格式；Discord 和 Slack 填写对应的传入 Webhook 地址，ntfy 填写允许发布的完整主题地址。

## 可选请求签名

管理员可单独设置或清除签名密钥；留空表示保留已有密钥。没有密钥时默认不签名，也不发送签名请求头。密钥为 8–1024 字节的 UTF-8 文本，不允许 ASCII 控制字符（U+0000–U+001F 和 U+007F）；空格也属于密钥的一部分。建议使用独立的高强度随机密钥，并通过安全渠道提供给接收方。页面和接口只显示是否已设置，不返回密钥内容。

管理接口 `GET /api/webhook` 只返回 `hasSigningSecret`，不返回密钥；`POST /api/webhook` 沿用现有的配置修订号及完整设置，并可通过 `signingSecret` 设置新密钥，或通过 `clearSigningSecret: true` 清除。省略或留空 `signingSecret` 会保留已有密钥；同一请求不能同时设置和清除。只有管理员能访问这两个操作。

设置密钥后，所有格式都在 `X-Kurumi-Signature-256` 请求头发送 `sha256=<64 位小写十六进制>`。计算方式为 `HMAC-SHA256(密钥的 UTF-8 字节, 实际发送的完整请求体字节)`；例如通用格式签 JSON 字节，ntfy 签文本字节，Telegram 签包含 `chat_id` 的最终 JSON，Gotify 签最终 JSON。接收方须在解析或重新序列化之前取得原始请求体，检查前缀、解码十六进制，再使用恒定时间比较验证；要求签名时应拒绝缺失、重复或格式错误的签名头以及验签失败的请求。请根据部署对读取的请求体设置大小限制。

以下 Go 片段展示验签核心（`header` 是该请求头，`body` 是原始请求体，`secret` 是配置密钥的 UTF-8 字节）：

```go
func validSignature(header string, body, secret []byte) bool {
    const prefix = "sha256="
    if !strings.HasPrefix(header, prefix) || len(header) != len(prefix)+64 {
        return false
    }
    signature, err := hex.DecodeString(header[len(prefix):])
    if err != nil {
        return false
    }
    mac := hmac.New(sha256.New, secret)
    _, _ = mac.Write(body)
    return hmac.Equal(signature, mac.Sum(nil))
}
```

该片段需要标准库的 `strings`、`encoding/hex`、`crypto/hmac` 和 `crypto/sha256`。签名只覆盖请求体，不覆盖请求方法或目标地址。通用 JSON 的 `at` 和各渠道消息中的时间是事件生成时的 UTC RFC 3339 时间，而非保证唯一的请求编号；即使它被包含在签名的请求体内，**签名和事件时间本身也不能防止重放**。需要防重放时，接收方须自行设计去重与有效期策略，并考虑时钟偏差及相同事件内容。

第三方渠道（包括 Telegram 和 Gotify）是否保留或验证自定义请求头由服务端决定，不能假定它们会验证签名。自建接收端或可信转发服务需自行启用验签；签名不会修改现有渠道的请求体格式或 Telegram 成功判定。

## Telegram

选择 Telegram 格式，并填写完整的 Bot API 地址，例如：

```text
https://api.telegram.org/bot<TOKEN>/sendMessage?chat_id=<CHAT_ID>
```

将占位符替换为 Bot 令牌和目标会话 ID。`chat_id` 可为数字 ID（包括负数）或 `@频道名`；地址只能包含一个 `chat_id` 查询参数，不支持其他查询参数。目标路径须以 `/bot<TOKEN>/sendMessage` 结尾；如使用自建 HTTPS 转发，须自行信任和保护该服务。发送时 `chat_id` 会从地址查询参数移入 JSON 请求体，与 `text` 一起提交；令牌仍在请求路径中。响应须为 HTTP 2xx 且 JSON 中的 `ok` 为 `true` 才计为成功。

## Gotify

选择 Gotify 格式，并填写包含应用令牌的完整消息地址，例如：

```text
https://push.example.com/message?token=<APP_TOKEN>
```

消息使用 JSON 请求体，包含 `title`、`message` 和 `priority`；标题为 `KurumiFRP`，优先级固定为 `5`。地址可以包含部署所需的路径和查询参数。HTTP 2xx 计为目标已接受请求。

## 存储与连接边界

通知地址可能包含令牌；地址和签名密钥都使用本机数据密钥加密保存，不在页面再次显示，也不包含在配置导出、快照或加密备份中。导入和恢复保留本机设置，并在数据密钥变更时重新加密二者；其他设备需单独配置。加密不能阻止有本机文件和账户权限的人员读取这些凭据。

所有渠道复用相同的公网地址检查：仅使用 HTTPS，不使用系统代理、不跟随重定向；发送时重新解析 DNS，拒绝含非公网 IP 的结果并直接连接已检查的地址，TLS 仍校验主机名。连接错误和响应内容不会回显到状态或错误消息。队列有限，满时会丢弃；设置变更、关闭或中断可能使消息丢失，不自动补发。地址、密钥或其他通知设置变更后，旧队列不会使用新配置；已经发往旧目标的请求会尝试取消，但已经交给网络的数据无法撤回。

旧版本若不认识 Telegram 或 Gotify 格式或签名密钥字段，会拒绝读取该通知设置并保留原文件。需要降级时，应先在新版本停用通知、清除地址与签名密钥，并将格式切回旧版本支持的通用 JSON 后保存；仅停用通知或清除地址并不会改变格式。
