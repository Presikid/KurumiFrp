# KurumiFRP 安装、目录与卸载

> 0.8 内置核心与安装包改造仍是开发候选，尚未完成实际设备、协议、并发、异常恢复及安装升级卸载验收，不可据此视为正式发行。以下 0.7.1.23 文件名仅说明已发布版本的安装形式。

0.8 默认构建将 frp 代码编入工作台，不下载或附带独立 frpc/frps 程序。已有用户核心目录不用于内置运行时；包管理器可能按其自身记录移除旧包拥有的预置文件。frp 许可证包含在 Windows 安装说明、Linux 包版权文件和便携包的 `LICENSE-frp` 中。

## 登录工作台

本分支的 0.8 开发版新安装时，管理员登录不填用户名，密码默认为空；登录后可在界面修改密码。快捷方式打开工作台；未登录时显示登录页面。

数据目录中尚无管理员密码文件时，初始管理员密码为空；已有的管理员密码文件和本地账户密码保留。登录后可设置主密码。本机管理令牌仍用于维护，应妥善保管；源码或便携版如使用了自定义数据目录，查看令牌时须传入相同的 `--data` 路径：

```powershell
& "$env:ProgramFiles\KurumiFRP\kurumi.exe" --show-token
```

Linux 原生包使用 `sudo kurumifrp --show-token`，源码版使用 `./kurumi --data <数据目录> --show-token`，便携版将 `./kurumi` 改为 `./kurumifrp`。这些命令会在终端显示敏感令牌，不要将输出发送给他人或写入共享日志。

## Windows：Program Files 正式安装

以下文件名以 0.7.1.23 安装包为例；请以 Releases 中实际已发布的版本和下载的文件名为准。

下载 `KurumiFRP-0.7.1.23-Setup.exe`，双击并允许 UAC 管理员授权。一个安装文件按系统选择 x64 或 ARM64。默认程序目录是 `C:\Program Files\KurumiFRP`（实际路径使用系统 Program Files 定义），注册到 HKLM 的“已安装的应用”，不是 AppData 内的当前用户安装。安装、打开和卸载均不依赖 PS1/CMD、Python、Go 或 Node.js。

程序文件和可变数据分离：

| 内容 | 位置 |
|---|---|
| 主程序、说明、安装登记、原生卸载器 | `%ProgramFiles%\KurumiFRP` |
| 账户隔离的配置、令牌、密钥、历史 | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\data` |
| 旧版外部核心（如有，内置模式不使用） | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\cores` |
| 工作台及托管 frp 的运行临时文件 | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\tmp\session-*` |
| 机器级目录所有权记录 | `%ProgramFiles%\KurumiFRP\runtime\machine-install.json` |

这是机器级安装、按 Windows 账户隔离的本机工作台，不是 Windows 系统服务。每个账户有独立配置、令牌和实例；应用自己的长期文件不另外放进该账户的 AppData/Documents。SID 是 Windows 安全标识，不是另建系统账户。安装目录中的 `runtime` 根目录由管理员创建并保护；普通用户只能创建并访问自己受 NTFS 权限保护的 SID 子目录，系统和管理员仍拥有管理权限。上表是新安装的默认路径；符合检查的旧版机器级安装可以继续使用 `%ProgramData%\KurumiFRP\profiles\<Windows SID>`。

安装向导可选公共桌面快捷方式和各用户登录时启动。安装结束后，登录平时使用的 Windows 账户，从开始菜单以普通权限手动打开工作台；输入了另一个管理员账户的 UAC 凭据时尤其如此。快捷方式直接运行 `kurumi.exe --open`。选择登录启动会在各账户登录时启动其独立工作台；默认不勾选。管理端只选择 9000–9010 中的本机回环端口，不对公网开放。

覆盖升级沿用登记的旧安装目录，并使用安装包内的新维护程序停止各账户工作台。旧 ProgramData 数据通过安装记录和 ACL 检查后继续在原位置使用，不自动搬迁或清理。旧目录不可确认、存在新旧两份数据、中断维护标记、链接、不支持的权限或工作台无法停止时，升级中止并保留数据；先备份并排查，不要删除这些目录或标记来强行继续。新安装目录必须为空；机器级安装使用有持久 ACL 的受保护本地目录。卸载前请先关闭所有账户的工作台和连接；新的卸载器保留账户数据，旧安装器的卸载文件也可能仍留在原目录，不要使用它们清理数据。

卸载后如需接回保留的数据，请手动选择原来的完整安装目录。安装器会先核对安装记录、数据根目录和账户资料的权限与记录，再停止仍在运行的工作台；检查失败时保留原文件并停止。卸载后自定义目录无法自动定位；如果选择另一个空目录，安装器可能创建新的空配置，原目录的数据不会自动接回。中断恢复的限制见 [Windows 报告](https://github.com/Presikid/KurumiFrp/blob/v0.7.1.23/docs/WINDOWS_0.7.1.23.md)。

安装程序不再自动验证工作台就绪或删除 Setup.exe；确认安装和配置后可自行保留或删除安装包。Inno Setup 的解包/卸载工作目录仍可能使用系统 Temp；浏览器缓存、Cookie、主题存储、快捷方式、系统审计和安全软件记录也不属于应用数据根目录。不能宣称全系统零写入或取证无痕。

尚未配置 Authenticode 代码签名，可能显示未知发布者或 SmartScreen 提示。不要因为扩展名是 EXE 就认为已有发行签名。

## Linux：使用发行版包管理方式

Debian/Ubuntu 按架构下载 DEB，Fedora/RHEL 系按架构下载 RPM：

```sh
sudo apt install ./kurumifrp_0.7.1.23_amd64.deb
# ARM64 改为 kurumifrp_0.7.1.23_arm64.deb

sudo dnf install ./kurumifrp-0.7.1.23-1.x86_64.rpm
# ARM64 改为 kurumifrp-0.7.1.23-1.aarch64.rpm
```

升级继续交给 APT/DNF。没有另外部署在线 APT/YUM 软件源，也未提供仓库 GPG 签名；这是上游发布的软件包，不表示已进入发行版官方仓库。附带 SHA-256 是完整性校验，不是独立身份签名。

Linux 原生包将程序和运行数据放在 `/opt/kurumifrp`。包管理器仍在系统目录登记服务、命令链接和文档。

| 内容 | 位置 |
|---|---|
| 主程序 | `/opt/kurumifrp/kurumifrp`（`/usr/bin/kurumifrp` 为命令链接） |
| 状态、密钥、历史与临时文件 | `/opt/kurumifrp/runtime` |
| 升级前数据备份 | `/opt/kurumifrp/backups/legacy`，仅 root 可访问 |
| 包安装归属记录与更新器工作目录 | `/opt/kurumifrp/meta`、`/opt/kurumifrp/private` |
| 服务单元和文档 | `/usr/lib/systemd/system`、`/usr/share/doc/kurumifrp`、`/usr/share/man` |

升级旧包时会停止服务，在安装目录内保留旧数据的备份，完成复制后切换运行目录；不符合安全检查或中途出错时，保留已有数据供排查。普通卸载保留数据，彻底清理会删除运行数据和迁移备份。工作台及托管实例的临时文件位于运行目录中，不修改系统全局 TEMP/TMP 环境。

系统服务由专用低权限 `kurumifrp` 账户运行，保留 `ProtectHome=yes` 和 `ProtectSystem=strict`。没有开放低端口权限、修改防火墙或触碰现有网站。

```sh
systemctl status kurumifrp
sudo systemctl start kurumifrp
sudo systemctl stop kurumifrp
sudo kurumifrp --paths
sudo kurumifrp --show-token
```

系统服务启动工作台并不等于自动启动所有保存的 frp 实例；只有启用了自动恢复的实例才会尝试启动。无桌面服务器通过 SSH 端口转发访问回环管理端。没有活动 systemd 的容器只完成包登记，不冒充服务已启动。

```sh
sudo apt remove kurumifrp  # 移除程序，保留状态和密钥
sudo apt purge kurumifrp   # 完整移除程序、托管数据和专用账户
sudo dnf remove kurumifrp  # 此 RPM 的最终卸载会清除托管数据；升级保留
```

DEB/RPM 不在包维护脚本里猜测、扫描并删除用户下载文件，不破坏包管理器缓存或事务记录。需要安装验证后删除当前包，可执行：

```sh
file=kurumifrp_0.7.1.23_amd64.deb
sudo apt install "./$file" && sudo kurumifrp --check-running && rm -- "./$file"
```

## 更多 Linux 架构

除 amd64 和 arm64 外，发布页提供 armhf、riscv64 的 DEB；按系统架构选择对应文件。系统安装包按上面的方式安装和更新。

计划构建 KurumiFRP-版本-linux-架构.tar.gz 便携包，覆盖 ARM 基线版、ARM 硬浮点版、RISC-V 64、LoongArch 64，以及 MIPS 大小端 32/64 位。解压后在可写目录运行 ./kurumifrp，可通过 --data 指定数据目录。管理页面默认只监听本机，通过 SSH 端口转发从其他设备访问。未设置过管理员密码的数据目录使用空密码；登录说明见上方「登录工作台」。

便携包不会注册系统服务。更新时先停止旧工作台并备份数据，将对应架构的新压缩包完整解压到新的空目录；启动新程序时通过 `--data` 指向原来的数据目录，建议使用绝对路径。若此前使用默认路径，它是旧进程工作目录下的 `data`。不要覆盖已有目录。ARM 基线包适合较老的 ARM 设备，arm_hf 包要求 ARMv7 硬浮点支持。MIPS 包按设备位数和大小端选择。

历史版本曾对这些架构的可执行文件进行模拟器启动验证；当前构建流程不包含该验证。模拟验证不等于在所有真实设备及发行版上完成安装、转发和性能验证。

## 临时文件与异常退出

工作台获得实例目录锁后，为本次运行创建专用临时会话，并向工作台设置 TMPDIR/TEMP/TMP。浏览器启动保留原环境，不把外部浏览器的临时文件接管到工作台。正常退出清理本次临时会话；下次成功启动只回收具有本产品标记的历史会话，不扫描系统 Temp。

异常断电可能留下旧进程锁或临时目录；本版不以删除未知进程锁的方式假装安全恢复，也不保证任意崩溃后完全无残留。内置运行时的异常恢复和资源清理仍需实际验收。

## 从 0.4 或更早版本迁移

旧用户级或脚本安装的数据不会自动提升为机器级数据。受支持的旧机器级 ProgramData 数据仍在原位置使用；无法确认安装记录或权限的旧数据保持原样，需先备份并人工处理。Linux 原生包升级时将旧运行数据迁移到安装目录，并在安装目录保留备份。

## 规范与验证

目录依据：微软 Shell 安全建议 https://learn.microsoft.com/en-us/windows/win32/shell/sec-shell ，Windows Known Folders https://learn.microsoft.com/en-us/windows/win32/shell/knownfolderid ，FHS https://refspecs.linuxfoundation.org/FHS_3.0/fhs/index.html ，systemd.exec https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html 。程序放 Program Files 不代表配置也应写入该只读目录。

历史版本的 CI 曾覆盖默认 Program Files 安装、HKLM 登记、真实普通账户使用、账户间密钥隔离、Program Files 写保护、临时路径、升级与完整卸载；Linux 曾覆盖实际 DEB/systemd 与 Fedora 容器 RPM 生命周期。以对应提交的 CI 结果为准。ARM64 构建不等于原生硬件验证；没有完成全系统文件访问追踪，不承诺操作系统、浏览器或第三方软件完全不产生外部记录。

候选 CI 构建 Windows 安装包、Linux 安装包和便携包，并编译 macOS 源码；自动发布已禁用，须完成实际验收后再评估启用。

## 配置迁移与软件更新

从 0.6.0 升级到四段版本号的版本时，请从 GitHub Releases 下载对应的原生安装包并手动安装；0.6.0 的内置更新器不识别四段版本号。升级前请先备份配置并停止实例。

工作台的当前实例配置、令牌、历史快照和快照数量统一存入 `state.json`。从配置中心导出的单个 JSON 文件可在另一台工作台导入。导入或恢复前请先停止全部实例并等待操作结束；导入完成后界面自动刷新，再按需启动实例。配置文件携带解密密钥，应像密码一样保管。工作台本地访问令牌不包含在导出的配置中；内置核心随工作台程序提供。旧版状态与密钥会在首次运行时迁移。

软件更新页面访问本项目的 GitHub Releases，列出可用新版本，并标明正式版、预发布或发布状态待确认；安装确认框也会显示该状态。默认选择可安装的正式版，预发布和状态待确认的版本仍可手动选择，状态待确认时请先核对发布页面。经确认后校验官方安装包的大小和 SHA-256，并使用原生安装机制升级。Windows 需要 UAC 授权，Linux 原生包使用受限的 systemd 更新任务和包管理器。更新会停止工作台及实例，源码运行版本不支持自动安装。
