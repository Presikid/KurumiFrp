# 内置服务端流量 / Embedded server traffic

## 统计含义

内置 `frps` 按运行代次维护一组独立的转发边界计数。**接收**表示从用户侧向代理侧转发，**发送**表示从代理侧向用户侧转发；两者是服务端视角，不是客户端 `/api/traffic` 的应用负载字节，也不是网络接口字节或计费数据。按代理名称和类型汇总，只有记录过正数字节的代理才会出现；同名同类型代理重连后仍归入同一项。每代最多保留 1024 个不同代理的明细；名称超过 256 字节或类型超过 32 字节的代理也不保留明细。省略明细时 `proxiesTruncated` 为 `true`，已保留的明细和全部代理总量仍继续累计。

计数范围取决于协议：流式传输可能包含应用协议帧，SUDP 等可能包含帧和心跳。UDP 统计的是实际记录的数据报内容，不代表服务端收到或成功转发的全部数据报。HTTP 的发送量是从代理工作连接读取的字节，不能保证已送达用户。绕过本服务端的 P2P 直连流量无法统计；经过服务端的回退转发仍按相应协议的计数范围记录。不要将这些数字视作精确的网络用量。

这里的运行计数从每次启动的新服务开始，快照本身不持久化。停止后，当前运行对象的快照仍可读取，但已接受连接的处理可能仍在退出并增加计数。下一次启动、删除实例、替换状态或退出工作台会丢弃该运行代次的快照。另有按日保存的采样增量，可能不完整，详见[流量历史](TRAFFIC_HISTORY.md)。计数饱和于 `uint64` 上限，达到上限后不再增加；单项和总量分别饱和。

## API

`GET /api/server-traffic?id=<实例 ID>` 使用工作台已有的认证、实例查看授权和流量读取权限。代理名称和类型属于配置详情，只有同时具有配置详情读取权限时才返回逐代理明细；否则仍可读取总量，响应的 `detailsAllowed` 为 `false`，不应把空明细当作没有代理。只能请求一个 `frps` 实例；未授权返回 403，实例不存在返回 404，类型错误或 ID 无效返回 400。读取期间实例或运行对象被替换时返回 409，可刷新状态后重试。自定义后端未提供计数、尚未创建内置服务或无运行对象时返回 `available: false`，计数为 `null`，而非零。

成功响应包含 `instanceId`、`runId`、`state`、`available`、`sampledAt`、`receivedBytes`、`sentBytes`、`proxies` 和 `proxiesTruncated`；不可用时 `reason` 为 `not_started` 或 `source_unavailable`。总量与每个代理的字节数都编码为十进制字符串，避免 JavaScript 的整数精度损失。快照在采集时一致；`state` 是读取后观察到的运行状态，之后仍可能变化。

```json
{
  "instanceId": "server-id",
  "runId": "run-id",
  "state": "running",
  "available": true,
  "sampledAt": "2026-01-01T00:00:00Z",
  "receivedBytes": "9007199254740993",
  "sentBytes": "2048",
  "proxiesTruncated": false,
  "detailsAllowed": true,
  "proxies": [{"name": "example", "type": "tcp", "receivedBytes": "9007199254740993", "sentBytes": "2048"}]
}
```

## English

The embedded `frps` collects separate per-generation forwarding-boundary counters. Received means user toward proxy, and sent means proxy toward user. They differ from the payload-only client `/api/traffic` counters and from network-interface or billing totals. Proxy entries are keyed by name and type and appear after a positive byte count; reconnects in the same generation reuse the entry. At most 1024 distinct proxy entries are retained per run; proxy names over 256 bytes or types over 32 bytes are also omitted from details. When any detail is omitted, `proxiesTruncated` is `true`; retained entries and the total for all proxies continue counting.

Scope varies by protocol: stream counts may include application framing, and SUDP may also include framing and pings. UDP counts recorded datagram contents, not every datagram received or forwarded. HTTP sent counts are bytes read from the proxy work connection and may not reach the user. Direct P2P traffic bypassing this server is not counted; a relayed fallback is counted according to that forwarding protocol. These values are not precise network-usage totals.

A new service starts new in-memory counters. After stop, the retained run can still be read, but accepted handlers may continue recording while they exit. Restarting the instance, deleting it, replacing state, or exiting the workspace discards that generation's snapshot. A separate daily history stores confirmed sample deltas with possible gaps; see [traffic history](TRAFFIC_HISTORY.md). Each total and proxy counter saturates independently at the `uint64` maximum.

`GET /api/server-traffic?id=<instance ID>` requires authentication, instance-view access and traffic-read permission. Proxy details additionally require configuration-read permission; otherwise totals remain available, `detailsAllowed` is false, and an empty detail list does not mean there are no proxies. Only one `frps` ID is accepted. It returns 403 for a missing view grant, 404 for an unknown ID, 400 for invalid ID or kind, and 409 when the instance or run changes while reading. A custom backend without counters, an as-yet-unconstructed embedded service, or an absent run produces `available: false` with null totals rather than zero. `reason` is `not_started` or `source_unavailable` when unavailable. All byte counters in JSON are decimal strings to preserve `uint64` precision. The counters are copied consistently; `state` is observed after the copy and can change afterward.
