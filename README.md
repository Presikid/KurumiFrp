<p align="center">
  <img src="assets/material/solo/KA15-bat-magic-gentle-v01.png" width="1000">
</p>

<h1 align="center">KurumiFRP</h1>

<p align="center">让狂三陪你整理 frp 连接 ♡</p>

<p align="center">
  <a href="https://github.com/Presikid/KurumiFrp/releases">下载安装</a> ·
  <a href="#第一次使用">第一次使用</a> ·
  <a href="#备份与迁移">备份与迁移</a> ·
  <a href="docs/INSTALL.md">详细安装说明</a>
</p>

KurumiFRP 是一个以时崎狂三为主题的本机 [frp](https://github.com/fatedier/frp) 管理工作台。在浏览器里配置规则、启动实例、查看运行记录，不必每次都手写配置文件。

工作台由 Go 编写，界面内嵌在程序中；日常运行不需要 Node.js、数据库服务器或 Docker。

> 主分支正在开发 0.8.0.0，内置核心及安装包改造尚未完成真实协议、多实例、异常恢复和安装升级验收，不可作为已发布版本。已发布的版本和实际安装包请以 [Releases](https://github.com/Presikid/KurumiFrp/releases) 页面为准。

## 下载安装

前往 [Releases](https://github.com/Presikid/KurumiFrp/releases) 下载对应系统的安装包。下方 Linux 命令以 0.7.1.23、amd64 文件名为例；如果下载的是其他版本或 ARM64 包，请替换为实际文件名。

**Windows 10 / 11**

下载目标版本的 `KurumiFRP-<版本号>-Setup.exe`，双击后按向导安装。安装需要管理员授权；之后可以从开始菜单或桌面快捷方式打开。安装程序会根据系统选择 x64 或 ARM64。

**Debian / Ubuntu**

```sh
sudo apt install ./kurumifrp_0.7.1.23_amd64.deb
```

**Fedora / RHEL 系**

```sh
sudo dnf install ./kurumifrp-0.7.1.23-1.x86_64.rpm
```

Linux 安装包会配置 systemd 服务；ARM64 请下载对应架构的包。安装、升级、卸载及旧版迁移的步骤见 [安装说明](docs/INSTALL.md)。目前安装包尚未进行商业代码签名，也没有在线 APT/YUM 软件源。

从 0.6.0 升级到四段版本号的版本时，请下载原生安装包手动升级；旧版内置更新器无法识别新的版本号。

## 第一次使用

1. **打开工作台。** Windows 可使用安装后的快捷方式；Linux 服务安装后，可用 `systemctl status kurumifrp` 查看运行状态。管理界面仅监听本机回环地址，端口为 9000–9010 中的可用端口。
2. **新建实例和规则。** 在「运行实例」中添加客户端或服务端，再根据服务器地址、端口和认证方式填写配置。frp 已内置于工作台，不需要单独下载。
3. **启动并查看记录。** 程序会先验证配置；启动后可在「运行记录」查看实例输出。实例状态不代表隧道已连通，实际链路还需要单独检查。

> KurumiFRP 管理的是运行它的这台机器上的 frp。使用内网穿透仍需要可访问的 frp 服务端及相应网络条件；它不会替你提供公网服务器。

Windows 快捷方式可以自动打开工作台；手动登录或无桌面环境下获取令牌、Linux 的 SSH 端口转发等方式见 [安装说明](docs/INSTALL.md)。

## 能做什么？

- **连接与实例**：管理本机多个客户端和服务端，配置 TCP、UDP、HTTP、HTTPS、TCPMUX、STCP、XTCP、SUDP 和 Visitor，查看实例状态与运行记录；客户端可选择连接协议并指定 DNS 服务器，留空时使用系统默认设置；可为每个实例选择在工作台启动时自动恢复。
- **本地目标诊断**：检测 TCP/STCP 端口或 HTTP/HTTPS 服务，可自定义诊断路径并批量检查；结果只代表本地目标状态，不代表远端隧道或公网可达。UDP 不支持通用探测。
- **代理自动健康检查**：可为代理规则设置 TCP 或 HTTP 检查及超时、间隔和连续失败次数；达到失败次数后停止代理，目标恢复后自动重新注册。HTTP 检查使用 GET，2xx 响应表示正常；此功能与手动本地诊断分别配置。
- **HTTP 自定义头**：分别设置请求头和响应头，头值加密保存且编辑时不回显；详情与导出注意事项见 [HTTP 自定义头说明](docs/HTTP_HEADERS.md)。
- **代理注册与链路监测**：内置客户端提供当前实例的已应用代理状态；状态不可用和待应用不会算成已注册。Visitor 不提供代理注册状态。链路监测检查实例、草稿控制端口和本地目标，并显示规则注册及汇总；结果不代表认证成功或公网可达。导出的诊断报告不含认证凭据，但含实例名称与网络地址，请妥善保管。
- **内置核心**：默认使用集成的 frp v0.72.0 代码，随工作台更新，不再下载或切换外部核心。受限功能和尚未完成的验收见 [内置运行时说明](third_party/frp/KURUMI_ISOLATION.md)。
- **配置差异**：对比实例上次应用的配置与当前草稿，查看变更；密钥内容不会出现在差异中。
- **本地账户与授权**：管理员可分配操作员、查看者及其实例范围，并可按账户单独允许查看这些实例的日志；权限说明见[本地账户与权限](docs/PERMISSIONS_0.8.md)。
- **配置与快照**：在界面中预览配置、导入导出，或恢复历史快照。默认保留 5 份，可调整为 0–100 份。
- **规则批量导出**：管理员可将所选规则按所属客户端导出为 frpc TOML 压缩包；文件含明文认证信息，须妥善保管。使用方式见[规则导出说明](docs/RULE_EXPORT.md)。
- **frp TOML 导入**：预览后将可映射的客户端或服务端配置追加为新草稿，不替换已有实例。模板、includes、插件和无法保留的字段会列出并阻止导入。
- **事件通知**：可选使用 Webhook 发送实例状态和配置变更，支持通用 JSON、Discord、Slack、ntfy、Telegram 和 Gotify 格式，默认关闭；地址和可选 HMAC-SHA256 签名密钥加密保存在本机。通知仅支持公网 HTTPS，不使用系统代理、不跟随重定向，也不支持单独设置认证请求头。消息只包含事件、时间和实例 ID / 类型或配置版本。地址填写与结果说明见[事件通知](docs/NOTIFICATIONS.md)。
- **远程节点状态与控制**：目标分别授权后，可通过可信 HTTPS 入口查看实例状态或确认启动、停止和重启实例；远程控制默认关闭，详见[节点说明](docs/REMOTE_NODES.md)。
- **软件更新**：已安装的 Windows 和 Linux 原生包可从工作台选择项目发布版本并更新；源码运行版本需要自行更新。
- **随手看看**：界面支持深浅主题和手机尺寸；运行记录保留当前运行代次最近 200 行实例输出，可按关键词筛选并将显示内容导出为文本。日志可能包含敏感信息，分享前请检查。

候选构建目标包括 Windows x64 / ARM64、Linux amd64 / arm64 的 DEB 和 RPM、Linux armhf / riscv64 的 DEB，以及其他 Linux 架构的便携版。交叉编译不等于目标设备、安装器或协议验收通过。

## 备份与迁移

在「配置中心」可以导出当前配置、全部快照或单个快照，也可以导入配置文件。实例、规则、密钥和快照等工作台数据统一保存在一个 `state.json` 中。

也可以使用单独密码导出加密备份，并在其他设备恢复。密码不会被保存，遗失后无法恢复；使用方式见[加密备份说明](docs/BACKUP.md)。

**导出文件包含恢复配置所需的密钥，请像密码一样保管，不要公开分享。** 导入或恢复前请停止全部实例，并等待正在进行的操作结束。导出文件不包含工作台程序；内置核心随工作台提供。

Windows 原生安装版的数据通常位于安装目录下的 `runtime/profiles/<Windows SID>`，并按 Windows 账户隔离；符合检查的旧版 ProgramData 数据继续保留在原位置使用。Linux 原生包的数据位于 `/opt/kurumifrp/runtime`。源码运行时默认使用当前目录下的 `data`。完整路径及卸载时的数据处理见 [安装说明](docs/INSTALL.md)。

## 从源码运行

需要 Go 1.26 或更新版本。以下示例适用于 Linux / macOS：

```sh
go build -o kurumi ./cmd/kurumi
./kurumi --data ./data --listen auto --open
```

Windows PowerShell：

```powershell
go build -o kurumi.exe ./cmd/kurumi
.\kurumi.exe --data ./data --listen auto --open
```

无桌面环境时，请使用启动信息中的本机地址，通过 SSH 端口转发访问；令牌可用 `kurumi --data ./data --show-token` 查看。请勿公开访问令牌或配置文件。

开发构建：

```sh
go build ./cmd/kurumi
```

## 使用前知道这些

- 保存配置不会自动改变正在运行的实例；导入或恢复配置后，需要按界面提示重新启动实例。
- 工作台和 frp 实例是两回事：实例默认手动启动；可在编辑窗口单独启用自动恢复，启动前仍会校验配置，失败原因显示在实例卡片。
- 软件更新会中断正在运行的实例。管理器异常退出后，请先确认旧进程状态，不要盲目删除进程锁。
- 工作台面向本机管理；远程节点控制须独立授权，设备 Agent、配额和定时策略尚未实现。

## 更多资料

- [安装、升级与卸载](docs/INSTALL.md)
- [架构说明](docs/ARCHITECTURE.md) · [历史验证记录](docs/VALIDATION.md) · [开发路线](docs/ROADMAP.md) · [审计核验与修复](docs/AUDIT_REMEDIATION.md)
- [主题素材说明](docs/ARTWORK.md)
- [版本约定](docs/VERSIONING.md)


