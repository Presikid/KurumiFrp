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


## 0.3 安装清理与卸载边界

本机 CLI 新增 `--check-running`、`--stop`、`--cleanup-package` 和可选 `--cleanup-archive`；这些不是远程 HTTP 管理接口。停止通过安装目录已有的令牌和回环地址请求当前管理器，等待关闭所有托管子进程后释放锁；关闭失败保留锁，卸载不绕过它。

清理使用固定平台文件白名单、分发文件摘要和原始压缩包成员核对，不递归删除源目录里的未知文件。用户安装目录通过所有权记录识别，外部快捷方式比较实际目标和参数，Linux 外部启动文件比较已记录摘要。用户删除或修改外部集成后不会靠名称直接替换或清理。卸载标记在进程退出与文件清理阶段拒绝新的工作台启动。

Windows 卸载脚本保留自身和所有权记录到其他文件清理完成，删除 junction 本身而不遍历目标；Linux 清理固定安装目录，不跟随内部符号链接并限制跨文件系统递归。当前实现不声称抵御已控制同一系统账户的并发恶意修改。

系统日志、浏览器数据库、用户备份和其他安装实例不属于本卸载器的控制范围。没有隐蔽删除审计记录、停用安全软件或整盘名称扫描。

## 0.4 原生发行

Windows 采用 Inno Setup 原生引擎，不执行 PS1/CMD；安装器根据 CPU 选择 Go GUI 子系统程序。原生维护命令仅处理安装记录确认的 data/cores 与指定摘要的 Setup.exe；安装引擎负责自身文件和系统集成项。Linux 通过 DEB/RPM 登记 /usr 文件和 systemd 服务，独立低权限账户持有 /var/lib/kurumifrp；root 专用账户所有权记录保存在独立目录。包维护脚本只处理固定目录，普通 remove/升级不等于 purge。


## Local target diagnostics

The authenticated `POST /api/probe` endpoint snapshots a saved tunnel under the
application mutex, then releases the mutex before network I/O. A two-second timeout
bounds each check so a slow local service does not block unrelated workbench actions.
TCP, STCP and STCP Visitor checks only establish a local TCP connection. HTTP and
HTTPS checks send a `HEAD /` request using the first configured custom domain as
the Host header and TLS server name; the socket is dialed directly to the configured
local target, environment proxies are disabled, redirects are not followed, and
normal certificate verification remains enabled. A 2xx/3xx response is reported as
healthy; a 4xx/5xx response is reported as responding, not unreachable. UDP is
explicitly unsupported because a generic UDP datagram cannot reliably prove that
an application is listening. None of these checks proves frp registration, remote
server reachability, or public Internet access.
