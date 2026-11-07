<p align="center">
  <img src="web/kurumi.webp" width="200" alt="时崎狂三与时钟的主题插画">
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

> 当前 0.7 是主题预览版本。已发布的版本和实际安装包请以 [Releases](https://github.com/Presikid/KurumiFrp/releases) 页面为准。

## 下载安装

前往 [Releases](https://github.com/Presikid/KurumiFrp/releases) 下载对应系统的安装包。下方 Linux 命令以 0.7.0、amd64 文件名为例；如果下载的是其他版本或 ARM64 包，请替换为实际文件名。

**Windows 10 / 11**

下载目标版本的 `KurumiFRP-<版本号>-Setup.exe`，双击后按向导安装。安装需要管理员授权；之后可以从开始菜单或桌面快捷方式打开。安装程序会根据系统选择 x64 或 ARM64。

**Debian / Ubuntu**

```sh
sudo apt install ./kurumifrp_0.7.0_amd64.deb
```

**Fedora / RHEL 系**

```sh
sudo dnf install ./kurumifrp-0.7.0-1.x86_64.rpm
```

Linux 安装包会配置 systemd 服务；ARM64 请下载对应架构的包。安装、升级、卸载及旧版迁移的步骤见 [安装说明](docs/INSTALL.md)。目前安装包尚未进行商业代码签名，也没有在线 APT/YUM 软件源。

## 第一次使用

1. **打开工作台。** Windows 可使用安装后的快捷方式；Linux 服务安装后，可用 `systemctl status kurumifrp` 查看运行状态。管理界面仅监听本机回环地址，端口为 9000–9010 中的可用端口。
2. **准备 frp 核心。** 进入「核心管理」，选择官方稳定版本和运行 KurumiFRP 的机器架构，点击「下载到本机」，完成校验后启用。切换核心前需要停止全部托管实例。
3. **新建实例和规则。** 在「运行实例」中添加 `frpc` 或 `frps`，再根据你的服务端地址、端口和认证方式填写配置。
4. **启动并查看记录。** 程序会先调用 frp 验证配置；启动后可在「运行记录」查看进程输出。运行状态表示进程是否存活，实际隧道是否连通还需要单独检查。

> KurumiFRP 管理的是运行它的这台机器上的 frp。使用内网穿透仍需要可访问的 frp 服务端及相应网络条件；它不会替你提供公网服务器。

Windows 快捷方式可以自动打开工作台；手动登录或无桌面环境下获取令牌、Linux 的 SSH 端口转发等方式见 [安装说明](docs/INSTALL.md)。

## 能做什么？

- **连接与实例**：管理本机多个 `frpc` / `frps`，配置 TCP、UDP、HTTP、HTTPS、STCP 和 Visitor，查看进程状态与运行记录；可为每个实例单独设置工作台启动时自动恢复。
- **核心管理**：从 frp 官方发布中选择、下载和切换核心版本，下载时校验文件；切换不会自动启动实例。
- **配置与快照**：在界面中预览配置、导入导出，或恢复历史快照。默认保留 5 份，可调整为 0–100 份。
- **软件更新**：已安装的 Windows 和 Linux 原生包可从工作台选择项目发布版本并更新；源码运行版本需要自行更新。
- **随手看看**：界面支持深浅主题和手机尺寸，运行记录保留当前会话最近的进程输出。

frp 核心支持自动安装 Linux、Windows、macOS 的 amd64 / arm64 版本；KurumiFRP 的预编译安装包面向 Linux 和 Windows 的 amd64 / arm64。核心最低支持 frp 0.60，具体验证情况见 [测试说明](docs/VALIDATION.md)。

## 备份与迁移

在「配置中心」可以导出当前配置、全部快照或单个快照，也可以导入配置文件。实例、规则、密钥和快照等工作台数据统一保存在一个 `state.json` 中。

**导出文件包含恢复配置所需的密钥，请像密码一样保管，不要公开分享。** 导入后界面会刷新，但已经运行的实例需要重新启动才能使用新配置。导出文件不包含 frp 核心程序，换机器后请另行安装适合目标系统的核心。

Windows 数据位于 `%ProgramData%\KurumiFRP` 下，并按 Windows 账户隔离；Linux 原生包的数据位于 `/var/lib/kurumifrp`。源码运行时默认使用当前目录下的 `data`。完整路径及卸载时的数据处理见 [安装说明](docs/INSTALL.md)。

## 从源码运行

需要 Go 1.23 或更新版本。以下示例适用于 Linux / macOS：

```sh
go build -o kurumi ./cmd/kurumi
./kurumi --data ./data --frp-dir ./frp --listen auto --open
```

Windows PowerShell：

```powershell
go build -o kurumi.exe ./cmd/kurumi
.\kurumi.exe --data ./data --frp-dir ./frp --listen auto --open
```

也可以在工作台的「核心管理」中下载核心。无桌面环境时，请使用启动信息中的本机地址，通过 SSH 端口转发访问；令牌可用 `kurumi --data ./data --show-token` 查看。请勿公开访问令牌或配置文件。

开发与测试：

```sh
go test ./...
go test -race ./...
go vet ./...
```

## 使用前知道这些

- 保存配置不会自动改变正在运行的实例；导入或恢复配置后，需要按界面提示重新启动实例。
- 工作台和 frp 实例是两回事：实例默认手动启动；可以在实例编辑窗口单独启用「工作台启动时自动启动」，启动前仍会执行官方配置校验，失败原因会显示在实例卡片中。
- 软件更新会中断正在运行的实例。管理器异常退出后，请先确认旧进程状态，不要盲目删除进程锁。
- 工作台面向本机管理；远程设备 Agent、配额和定时策略等功能目前尚未实现。

## 更多资料

- [安装、升级与卸载](docs/INSTALL.md)
- [架构说明](docs/ARCHITECTURE.md) · [测试说明](docs/VALIDATION.md) · [开发路线](docs/ROADMAP.md)
- [主题素材说明](docs/ARTWORK.md)

时崎狂三是《约会大作战》中的角色；本项目的主题插画为非官方同人创作，相关角色权利属于各自权利方。项目目前未指定开源许可证。
