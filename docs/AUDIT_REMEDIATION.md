# 代码审计核验与修复说明

审计基线为 `main` 的 `6bb193cd962140a03c25c50f8d07f5bbd16de08e`。本次逐项核对了外部报告、实际调用路径和官方 FRP 行为，并检查了当时开放的 PR #4–#12。审计结论与“未来功能愿望清单”分开处理，避免用未经验证的建议替换已工作的实现。

## 本次修改

### TOML 与凭据

- 用 TOML basic string 编码器替换 JSON 编码器，显式转义 ASCII 控制字符及 DEL（U+007F）；保留合法 Unicode。
- 继续将**完整 TOML 字符串字面量**存入 `KURUMI_*` 环境变量，配置中的 `{{ .Envs.KEY }}` 不再套额外引号。该设计不经过 shell；模板展开一次后即成为正确的 TOML 字符串。
- 拒绝加密或解密后不是有效 UTF-8 的凭据，避免渲染时默默变成替代字符，导致实际密码改变。
- 回归测试覆盖引号、反斜杠、等号、中文、看起来像模板的密码、STCP/Visitor 和预览脱敏。真实核心测试同时验证 TCP 与 STCP 转发。

现有输入校验已经拒绝 ASCII 控制字符；DEL 修复完善的是底层编码与导入密文的边界，不能据此声称正常输入的密码普遍不可用。

### 生命周期与并发

- `/api/control` 与 `/api/validate` 单独执行；快照读取、最终一致性检查和进程发布才持全局状态锁。外部 `verify`、停止等待和运行配置文件准备在锁外进行。
- 同一实例同时只能有一个运行操作；重复请求及时返回冲突，不把过期启动请求排成长队。其他实例、草稿编辑及状态查询仍可继续。
- 验证后，以及停止/文件准备完成后，重新核对 revision、渲染摘要、核心选择、二进制路径和进程身份；不启动已经过期的草稿，也不在验证失败时先杀掉正常实例。
- 关闭工作台会阻止新的运行操作、取消在途校验并等待操作退出，然后停止所托管进程。
- 支持 POSIX 进程组的平台使用独立进程组；停止先通知整组，超时后强制清理整组；校验的上下文取消也会清理子进程组。其他平台保留对应回退。
- Windows 无窗口进程保留有时限的终止回退，不将无效的 Ctrl+Break 调用包装为“优雅退出”。本次没有新增 Windows Job Object 或 FRP Admin stop 控制通道。

### 状态轮询与界面

- `/api/state` 缓存已经脱敏的完整响应。缓存键来自配置 revision、核心选择、进程状态快照及二进制可用性，避免手工计数器在导入、退出或核心变化时漏更新。
- 响应带 ETag；认证后的条件请求在状态未变时返回空体 `304`，复用已序列化内容，也跳过重复解密/渲染 pending 摘要。快照锁不覆盖网络响应写入。
- `/api/logs` 保持独立：新日志不会错误地当作配置变化，前端也不会因状态返回 `304` 而停止刷新日志。
- 前端串行/合并状态刷新，保留手动操作后的新请求；登录状态变化后丢弃过期响应。
- 静默刷新比较最后实际显示的快照，在输入、弹窗及文字选区存在时保留当前 DOM；解除交互后仍会显示此前延期的更新。
- 保留已有 Blob URL 延迟释放逻辑，并验证导出清理及无变化刷新。

### 存储、脱敏与登录频控

- `writeIfChanged` 在目标为私有普通文件且内容未变时跳过替换；权限不合适时重新安全写入。拒绝符号链接及非普通文件。
- **变化的持久状态仍使用临时文件、fsync 和原子替换。** 不把 `state.json` 改为直接截断覆写。
- 保留独立的 verify 临时配置，避免验证草稿时覆盖正在运行实例的配置。运行时重复启动使用内容相同跳过写入。
- 日志脱敏预编译替换器，覆盖具有共同前缀的密钥及转义形式；按较长字符串优先，避免短密钥先替换后泄露长密钥后缀。
- 登录来源计数表严格限制容量，只清理到期计数，不淘汰仍生效的限流记录。表满不再连带封锁表内其他正常来源；清理工作限频，当前来源的过期记录立即重置。

## 对原报告的逐项结论

| 项目 | 核验与处理 |
| --- | --- |
| BUG-01 环境变量“双重引号” | 原报告没有按实际模板上下文分析。主分支使用无引号占位符，Env 中完整引号正确。保留设计、补注释及实际展开回归；不采用删除外层引号的修复。 |
| BUG-02 JSON 与 TOML 转义 | 采纳专用编码器。真实差异是 DEL；`\\uXXXX`、`\\b`、单引号、U+2028/U+2029 不是报告所说的不兼容原因。另增加无效 UTF-8 拒绝。 |
| BUG-03 进程管理 | 采纳 POSIX 进程组与有界停止。Windows 建议不成立：无控制台进程不因 OR 一个标志就与调用者共享控制台。 |
| BUG-04 Blob 与重绘 | `download()` 主分支已有 `revokeObjectURL`，并非未释放；修复仍存在的重复重绘、选区、延期更新与轮询并发问题。 |
| OPT-01 磁盘写入 | 采纳未变化跳过写入，保留配置持久性的原子替换；不以“零磨损”描述文件系统行为，不原地截断状态文件，不复用活动配置路径做草稿验证。 |
| OPT-02 全局锁 | 采纳校验、停止等待移出全局锁，同时补上并发操作预约、过期快照拒绝和关闭协调；不能仅把 Unlock 移到等待前。 |
| OPT-03 日志与 ETag | 采纳预编译脱敏与条件请求。主分支 `/api/state` 原本就不包含日志，不能把它描述为每轮传输所有历史日志。 |

## 第二份报告的功能核验

| 说法或建议 | 当前结论 |
| --- | --- |
| 缺少 TLS 开关，所以默认未加密 | 渲染已有 frpc `transport.tls.enable = true` 和 frps `transport.tls.force = true`。自定义证书/CA/双向认证属于额外功能。 |
| HTTP2 是可选 FRP transport | 官方配置列举 `tcp`、`kcp`、`quic`、`websocket`、`wss`，没有该选项。`https2http` 的 2 表示 to，也不等于 HTTP/2。 |
| 添加或启停一条规则立即重启全实例 | 实际为先保存草稿，只有明确 start/restart 才应用。热重载是新功能，不能当作这个不存在的自动重启 Bug 的补丁。 |
| 下载低于 0.52 的核心会不兼容 TOML | 当前 `validTag()` 及 Release 获取已要求 v0.60.0 或更高，不存在报告所述的下载入口。 |
| 缺少实时代理注册状态 | 主分支仍明确只显示进程存活；开放 PR #9、#11 已分别实现 Admin API 注册状态，需统一后合并，本次不引入第三套。 |
| 查询 `/api/proxy/status` | 兼容目标 FRP 的实际接口是 `GET /api/status`；地址字段为 `local_addr` / `remote_addr`。 |
| QUIC/KCP、XTCP/SUDP/TCPMUX、客户端插件、HTTP 路由等 | 确属当前模型尚未开放的能力，应分别补齐模型、校验、保密/导出、UI、官方核心集成测试；本次可靠性修复未声称新增。 |

HTTP header 修改、HTTP Basic Auth 和按路径路由不能直接套用到只透明转发 TLS 的 HTTPS proxy。负载均衡的现代键是 `loadBalancer.group` / `loadBalancer.groupKey`，并非旧格式的任意扁平字段。

热重载尤其需要单独设计：旧 frpc 进程的环境变量不会随草稿修改；新增/变更 STCP 密钥后直接调用 reload，可能读不到新变量或继续用旧值。共同连接参数也不能假设会由 proxy/visitor reload 一并生效。

## 未合并 PR 的整合注意

- #5 与 #6：自动启动重复，需与本次关闭门闩及操作管理合并为一套，防止关闭后又启动。
- #7 与 #8：配置差异 API/运行快照重复，不能直接并列加入同名路由及字段。
- #9 与 #11：Admin 接口注入重叠；应统一端口与凭据管理。#9 的地址 JSON tag 使用 camelCase，应对照官方 snake_case 修正；#11 的独立前端状态轮询也应复用本次重绘保护。
- #4：保留配置 revision 变化后清除本地探测结果的行为。
- #10：合并时将备份弹窗纳入交互保护；耗时密码派生还需独立审查锁范围。
- #12：复制操作保留原有配置事务和版本检查。

本次 PR 从 main 独立提交，没有替用户合并、关闭、重写这些功能 PR。

## 依据与验证

- [TOML 1.0 字符串规范](https://toml.io/en/v1.0.0#string)
- [FRP 客户端配置](https://gofrp.org/en/docs/reference/client-configures/)
- [FRP 通信安全与优化](https://gofrp.org/en/docs/features/common/network/network/)
- [FRP v0.60.0 Admin API 实现](https://github.com/fatedier/frp/blob/v0.60.0/client/admin_api.go)
- [Microsoft GenerateConsoleCtrlEvent](https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent)
- [Microsoft Process Creation Flags](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)

回归入口：`go test ./...`、`go test -race ./...`、`go vet ./...`、前端 Node 回归、浏览器 smoke、`python3 scripts/frp-smoke.py ./kurumi /path/to/frp`。真实核心验证覆盖复杂凭据、TCP 与 STCP/Visitor 往返传输和停止。具体执行结果见 PR 的验证记录；Windows 原生行为以 Windows CI 结果为准，交叉编译不等于 Windows 运行验收。
