# KurumiFRP 0.6

本机 frpc/frps 管理工作台。Go 标准库后端 + 内嵌中文响应式 WebUI，无 Node.js、数据库服务器或 Docker 运行依赖。

## 正式安装方式

**Windows 10/11**：下载 Releases 的 `KurumiFRP-0.6.0-Setup.exe`，双击图形化安装向导。自动选择 x64/ARM64，支持快捷方式、可选登录启动、应用列表和原生卸载，不需要 PowerShell/Python/Go。默认通过管理员授权安装到 **Program Files**，注册机器级卸载项。配置、密钥、核心和临时文件集中在 **ProgramData\KurumiFRP**，按 Windows 账户隔离；日常运行不向程序目录写入，也不要求持续提权。

**Debian/Ubuntu**：`sudo apt install ./kurumifrp_0.6.0_amd64.deb`。
**Fedora/RHEL 系**：`sudo dnf install ./kurumifrp-0.6.0-1.x86_64.rpm`。
ARM64 提供对应原生包。Linux 安装为低权限 systemd 服务，不再是 HOME 目录中的便携包；数据位于 `/var/lib/kurumifrp`，管理端仍只监听回环 9000–9010。

Windows 成功安装后可以自动删除 Setup.exe。Linux 原始 DEB/RPM 由调用者管理，不在包事务里猜测删除；安装成功验证后删除、完整卸载、旧版迁移和服务管理见 [安装说明](docs/INSTALL.md)。包管理器负责的软件不通过自删程序文件卸载。此版本尚无商业代码签名或 APT/YUM 在线仓库。

## WebUI 下载 frp 核心

打开 **核心管理**，选择官方稳定版本和系统/架构，点击 **下载到本机**；校验完成后点击 **启用此版本**。识别的是运行 KurumiFRP 的主机，不是访问网页的手机/电脑。其他平台可获取官方下载链接，但不能误装到本机。

下载任务显示真实字节进度，可取消，不阻塞配置编辑。安装检查官方 SHA-256、体积、归档路径及 ELF/PE/Mach-O 架构；frpc/frps 成对存入独立版本目录。旧核心保留，切换前必须停止全部托管实例；探测失败不改变活动版本，切换不自动启动实例。源码运行仍支持显式指定 `--frp-dir`；Windows 原生安装版使用受权限保护的 ProgramData 核心目录，不接受另行覆盖数据路径。

支持自动安装 Linux/Windows/macOS amd64/arm64 的官方核心，管理器预编译安装包发布 Linux/Windows amd64/arm64。frp 最低版本 0.60，实际验证版本见 CI。下载只访问 GitHub 官方端点，可通过进程的 `HTTPS_PROXY` 使用自己的代理；不会静默使用第三方镜像或跳过 TLS 检查。

## 工作台功能

网络总览、连接看板、运行实例、配置中心、核心管理和运行记录；深浅主题及手机布局。

多个本机 frpc/frps；TCP、UDP、HTTP、HTTPS、STCP 和 Visitor；带宽限制；revision 并发检查；历史快照（默认保留 5 份，可设为 0–100）；配置导入和导出。

实例、规则、密钥、核心选择、快照及保留设置保存在同一个 `state.json`；历史数据从旧格式迁移。导出文件可直接导入另一台机器。文件包含解密密钥，任何持有文件的人都能读取其中的令牌，应按敏感凭据保管。导入后工作台自动刷新；已运行实例在重启应用前继续使用原配置。

软件更新页面从本项目 GitHub Releases 获取版本，确认后下载对应安装包并校验 SHA-256。Windows 通过 UAC 启动安装器；Linux 原生包通过 systemd 更新服务和包管理器安装。更新会中断运行中的实例。源码运行版本需自行更新。

保存不等于应用。启动前执行官方 `frp verify`，候选配置校验失败不停止旧进程。状态显示真实 PID/退出信息，不将进程存活冒充隧道连通。管理 API 需要会话或本机 Token，校验 Host/Origin，无任意 shell 接口。

## 从源码运行

需要 Go 1.23+；CI 使用当前稳定 Go。

```sh
go build -o kurumi ./cmd/kurumi
./kurumi --data ./data --frp-dir ./frp --listen auto --open
```

无桌面环境时，通过 SSH 转发输出的本机端口，再执行 `kurumi --data ./data --show-token` 获取备用登录凭据。不要公开管理 Token、配置文件或数据目录。

```sh
go test ./...
go test -race ./...
go vet ./...
python3 scripts/package-native.py linux
```

`--listen 127.0.0.1:9000` 可固定端口。原生安装使用系统程序目录和专用数据目录，与源码部署的 `./data` 不是同一路径。`--paths` 可查询实际位置。Windows 从 0.4 用户版迁移需要先备份、卸载旧版；不自动执行或接管旧用户目录中的程序。完整升级方式见安装说明。

## 当前边界

这是本机管理预览版，不是云端控制器。未实现远程设备 Agent、frp Store 热更新、实例自动开机恢复、配额和定时策略。管理器的工作台启动入口不等于 frp 实例自动启动。导出的配置文件不包含 frp 核心程序，目标机器需另外安装相同的核心；不同系统架构需要选择兼容的核心。管理器崩溃后须确认旧进程状态再处理锁，不能盲目删锁。

架构见 [ARCHITECTURE](docs/ARCHITECTURE.md)，测试边界见 [VALIDATION](docs/VALIDATION.md)，路线见 [ROADMAP](docs/ROADMAP.md)。本项目未指定开源许可证；不要推断为 Apache/MIT。frp 是独立官方程序。
