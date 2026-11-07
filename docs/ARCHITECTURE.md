# Architecture and security boundary

The Go executable embeds `web/` using go:embed. No npm, Docker or remote service
is needed. All manager API operations use a single serialized application mutex;
process exits/log capture use independent locks. Per-object revision writes reject
stale forms with HTTP 409. Configuration mutation is not a process mutation.

`state.json` holds the current structured configuration, encrypted secret values,
30 previous states and 200 configuration events. Writes use temporary files, sync
and rename; readers only use the published in-memory state after a successful write.
Crash-durability details of rename/fsync differ by OS; this is not a transactional
multi-host database. A single-owner lock prevents accidental concurrent managers.

AES-GCM ciphertext is authenticated using a private 32-byte local master key.
Runtime TOML contains Go-template environment references, not raw credentials;
the child receives TOML-escaped values in its environment. The same OS account and
administrators can inspect process memory/environment and read the key. This design
protects against casual disclosure of state files, not against a compromised host.
Avoid unrestricted backups, process dumps and diagnostic environment exports.

The public configuration view strips ciphertext and plaintext. Blank secret fields
on edits preserve existing values. All instance auth tokens and STCP secrets must
have at least eight bytes. Session cookies stay in memory and expire after twelve
hours; a manager restart invalidates sessions. Logging out revokes that session, not
the local access token. To rotate the access token, stop the manager, remove only
`access.token`, and restart. Do not remove `master.key`.

Input is typed and rendered; no raw TOML uploads, plugin/exec configuration, browser
binary paths, shell commands or unauthenticated control endpoints are supported.
frp verification and launch execute only the operator-installed binary with fixed
arguments and no shell. The binary itself is trusted and must not be web-writable.
User-selected target addresses are intentional tunnel configuration, not constrained
by a network sandbox. frps proxy endpoints may publish services; choose them carefully.

Generated frp transport enables TLS; it does not provision a custom CA, certificate
pinning or mutual TLS. Do not infer peer identity verification from encryption alone.
A future connection policy layer must add those controls without weakening defaults.

Apply sequence: render -> bounded official verify -> optional stop -> spawn ->
observe real process exit. Verify failure leaves the old process alone. A successful
spawn is not a registration check. There is no automatic rollback on post-spawn failure.
Future hot reload must distinguish proxy changes from global process settings.

Logs are bounded, known secrets are scrubbed across fragmented writes, and oversized
lines are dropped. Logs are memory-only and are not a complete durable audit trail.
Only saved draft changes currently populate the persistent configuration event list.

References reviewed for the implementation:
- https://gofrp.org/en/docs/features/common/configure/
- https://gofrp.org/en/docs/features/common/client/
- https://gofrp.org/en/docs/reference/server-configures/

## 0.2 核心管理

`internal/core` 使用单独的任务锁和 context 处理官方网络下载，配置 API 不会持有应用锁等待下载。归档仅允许两个已知核心文件名及 LICENSE 写入临时目录，拒绝链接、路径穿越、重复、超大或缺失文件。下载前从官方发布 API 取得 SHA-256，校验完成才提取；不接受用户输入下载 URL。

已验证文件位于 `frp-dir/versions/<tag-platform-digest>/`，带 manifest；活动核心是 `active.json` 的单个指针。下载从不替换活动版本。启用在应用进程锁内检查全部实例停止、重新校验文件、执行两个核心的版本探测后再原子更新指针。磁盘与运行时生命周期由同一个管理进程拥有，不支持多个管理器共用同一个核心目录。

启动票据只有 60 秒寿命且使用后删除，通过 URL fragment 交给网页，网页先清除 fragment 再换取 HttpOnly 会话。长期 Token 不进入浏览器地址。安装器用户目录的权限保护很重要：加密不防同一系统账户或系统管理员访问本机主密钥。
