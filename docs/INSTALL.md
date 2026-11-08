# KurumiFRP 0.7 安装、目录与卸载

## Windows：Program Files 正式安装

下载 `KurumiFRP-0.7.1.4-Setup.exe`，双击并允许 UAC 管理员授权。一个安装文件按系统选择 x64 或 ARM64。默认程序目录是 `C:\Program Files\KurumiFRP`（实际路径使用系统 Program Files 定义），注册到 HKLM 的“已安装的应用”，不是 AppData 内的当前用户安装。安装、打开和卸载均不依赖 PS1/CMD、Python、Go 或 Node.js。

程序文件和可变数据分离：

| 内容 | 位置 |
|---|---|
| 主程序、说明、安装登记、原生卸载器 | `%ProgramFiles%\KurumiFRP` |
| 账户隔离的配置、令牌、密钥、历史 | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\data` |
| 下载的 frpc/frps 及版本清单 | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\cores` |
| 工作台及托管 frp 的运行临时文件 | `%ProgramFiles%\KurumiFRP\runtime\profiles\<Windows SID>\tmp\session-*` |
| 机器级目录所有权记录 | `%ProgramFiles%\KurumiFRP\runtime\machine-install.json` |

这是机器级安装、按 Windows 账户隔离的本机工作台，不是 Windows 系统服务。每个账户有独立配置、令牌和实例；应用自己的长期文件不另外放进该账户的 AppData/Documents。SID 是 Windows 安全标识，不是另建系统账户。普通用户对 Program Files 没有写权限；安装目录中的 `runtime` 根目录由管理员创建并保护；普通用户只能创建并访问自己受 NTFS 权限保护的 SID 子目录，系统和管理员仍拥有管理权限。运行时路径由当前可执行文件所在的安装目录解析，不依赖 AppData、ProgramData 或用户环境变量。

安装向导可选公共桌面快捷方式、各用户登录时启动，以及成功后删除 Setup.exe。启动由安装器以原始用户身份执行，常规使用不要求持续管理员权限。快捷方式直接运行 `kurumi.exe --open`。选择登录启动会在各账户登录时启动其独立工作台；默认不勾选。管理端只选择 9000–9010 中的本机回环端口，不对公网开放。

同一机器级安装覆盖升级会先停止各账户的托管实例，保留全部配置和密钥，再更新程序。首次从旧版升级时，安装器会验证旧 `%ProgramData%\KurumiFRP` 的安装记录、目录 ACL 和各账户 SID 记录，然后把完整 profile（配置、令牌、主密钥、快照、核心和临时目录）迁移到新的 `runtime`。同卷优先移动，跨卷时复制并校验 SHA-256；成功后才清理旧目录。若发现归属不明、权限异常、符号链接/junction、文件冲突或旧进程无法停止，安装会中止并保留旧数据；此时不要手动删除旧目录，应重新运行安装器或先备份后排查。迁移完成前新版不会启动空白配置。卸载使用 Windows“设置 → 应用 → 已安装的应用 → KurumiFRP”，需要管理员授权，并明确确认删除所有账户的配置、密钥及核心。停止未确认、目录归属或访问权限不符时中止，不把局部清理当作成功。不会按进程名杀掉其他 frp，也不会跟随符号链接/junction 删除外部目标。

安装成功并通过本机认证接口验证后，才安排清理本次准确路径、SHA-256 匹配的 Setup.exe。无法删除被持续占用的文件时保留它。Inno Setup 的解包/卸载工作目录仍可能使用系统 Temp；浏览器缓存、Cookie、主题存储、快捷方式、系统审计和安全软件记录也不属于应用数据根目录。不能宣称全系统零写入或取证无痕。

尚未配置 Authenticode 代码签名，可能显示未知发布者或 SmartScreen 提示。不要因为扩展名是 EXE 就认为已有发行签名。

## Linux：使用发行版包管理方式

Debian/Ubuntu 按架构下载 DEB，Fedora/RHEL 系按架构下载 RPM：

```sh
sudo apt install ./kurumifrp_0.7.1.4_amd64.deb
# ARM64 改为 kurumifrp_0.7.1.4_arm64.deb

sudo dnf install ./kurumifrp-0.7.1.4-1.x86_64.rpm
# ARM64 改为 kurumifrp-0.7.1.4-1.aarch64.rpm
```

升级继续交给 APT/DNF。没有另外部署在线 APT/YUM 软件源，也未提供仓库 GPG 签名；这是上游发布的软件包，不表示已进入发行版官方仓库。附带 SHA-256 是完整性校验，不是独立身份签名。

Linux 不照搬 Windows 的“一个安装文件夹”，而按文件性质分开：

| 内容 | 位置 |
|---|---|
| 包管理器拥有的主程序 | `/usr/bin/kurumifrp` |
| 服务单元 | `/usr/lib/systemd/system/kurumifrp.service` |
| WebUI 管理的状态数据库、密钥、历史与生成配置 | `/var/lib/kurumifrp/data` |
| 下载的核心及原子下载暂存 | `/var/lib/kurumifrp/cores` |
| 应用和子进程运行临时文件 | systemd 管理的 `/run/kurumifrp/session-*` |
| 包安装归属记录 | `/var/lib/kurumifrp-package` |
| 文档和 man 手册 | `/usr/share/doc/kurumifrp`、`/usr/share/man` |

没有 systemd 的显式手动运行使用 `/var/lib/kurumifrp/tmp`，不会回退到个人 HOME。`/var/lib` 内的是通过 WebUI 更新的状态数据库与生成内容，不是要求管理员手工编辑的 `/etc` 配置文件。目前没有单独的手工系统配置文件，因此不创建空的 `/etc/kurumifrp` 或无用途的缓存目录。

系统服务由专用低权限 `kurumifrp` 账户运行；`ProtectHome=yes`、`ProtectSystem=strict` 保留，主要持久可写区只有专用状态目录，临时目录由 systemd 管理。停止后回收 RuntimeDirectory，不修改系统全局 TEMP/TMP 环境。没有开放低端口权限、修改防火墙或触碰现有网站。

```sh
systemctl status kurumifrp
sudo systemctl start kurumifrp
sudo systemctl stop kurumifrp
sudo kurumifrp --paths
sudo kurumifrp --show-token
```

系统服务启动工作台并不等于自动启动所有保存的 frp 实例；实例自动恢复仍未实现。无桌面服务器通过 SSH 端口转发访问回环管理端。没有活动 systemd 的容器只完成包登记，不冒充服务已启动。

```sh
sudo apt remove kurumifrp  # 移除程序，保留状态和密钥
sudo apt purge kurumifrp   # 完整移除程序、托管数据和专用账户
sudo dnf remove kurumifrp  # 此 RPM 的最终卸载会清除托管数据；升级保留
```

DEB/RPM 不在包维护脚本里猜测、扫描并删除用户下载文件，不破坏包管理器缓存或事务记录。需要安装验证后删除当前包，可执行：

```sh
file=kurumifrp_0.7.1.4_amd64.deb
sudo apt install "./$file" && sudo kurumifrp --check-running && rm -- "./$file"
```

## 更多 Linux 架构

除 amd64 和 arm64 外，发布页提供 armhf、riscv64 的 DEB；按系统架构选择对应文件。系统安装包按上面的方式安装和更新。

还提供 KurumiFRP-版本-linux-架构.tar.gz 便携包，覆盖 ARM 基线版、ARM 硬浮点版、RISC-V 64、LoongArch 64，以及 MIPS 大小端 32/64 位。它们解压后无需安装：在可写目录运行 ./kurumifrp，用 ./kurumifrp --show-token 查看本机访问令牌。也可通过 --data 和 --frp-dir 指定数据和核心目录。管理页面默认只监听本机，通过 SSH 端口转发从其他设备访问。

便携包不会注册系统服务，程序更新需先停止工作台，再下载相应架构的新版并手动替换可执行文件；配置与已下载核心保存在独立的数据和核心目录。ARM 基线包适合较老的 ARM 设备，arm_hf 包要求 ARMv7 硬浮点支持。MIPS 包按设备位数和大小端选择。

这些新增架构的可执行文件经模拟器启动验证；模拟验证不等于在所有真实设备及发行版上完成安装、转发和性能验证。

## 临时文件与异常退出

工作台获得实例目录锁后，为本次运行创建专用临时会话，并向自身及 frp 子进程设置 TMPDIR/TEMP/TMP。浏览器启动保留原环境，不把外部浏览器的临时文件接管到工作台。正常退出清理本次临时会话；下次成功启动只回收具有本产品标记的历史会话，不扫描系统 Temp。核心下载暂存仍在 cores 下，便于同一文件系统原子启用。

异常断电可能留下旧进程锁或尚未清理的下载目录；本版不以删除未知进程锁的方式假装安全恢复，也不保证任意崩溃后完全无残留。第三方核心不受文件系统沙盒约束，环境重定向不是阻止其任意写入的安全边界。

## 从 0.4 或更早版本迁移

以下是 0.5 发布时的历史迁移说明：当时安装器不会自动执行旧用户目录中的程序，也不会把旧数据不经确认提升为机器级安装。当前版本已改为自动迁移受安装回执、SID 和 ACL 验证的旧 ProgramData 数据；从旧版升级时不再手动把备份放回 ProgramData，而由当前安装器迁移到安装目录的 `runtime`。Linux 从 0.4 的原生包升级仍保留原 `/var/lib/kurumifrp` 数据。

## 规范与验证

目录依据：微软 Shell 安全建议 https://learn.microsoft.com/en-us/windows/win32/shell/sec-shell ，Windows Known Folders https://learn.microsoft.com/en-us/windows/win32/shell/knownfolderid ，FHS https://refspecs.linuxfoundation.org/FHS_3.0/fhs/index.html ，systemd.exec https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html 。程序放 Program Files 不代表配置也应写入该只读目录。

CI 目标覆盖默认 Program Files 安装、HKLM 登记、真实普通账户使用、账户间密钥隔离、Program Files 写保护、临时路径、升级与完整卸载；Linux 覆盖实际 DEB/systemd 与 Fedora 容器 RPM 生命周期。以对应提交的 CI 结果为准。ARM64 构建不等于原生硬件验证；没有完成全系统文件访问追踪，不承诺操作系统、浏览器或第三方软件完全不产生外部记录。

## 配置迁移与软件更新

从 0.6.0 升级到四段版本号的版本时，请从 GitHub Releases 下载对应的原生安装包并手动安装；0.6.0 的内置更新器不识别四段版本号。升级前请先备份配置并停止实例。

工作台的当前实例配置、令牌、历史快照和快照数量统一存入 `state.json`。从配置中心导出的单个 JSON 文件可在另一台工作台导入，导入完成后界面自动刷新；已经运行的实例需重启才能应用新配置。配置文件携带解密密钥，应像密码一样保管。工作台本地访问令牌及 frp 核心程序属于本机运行数据，不包含在导出的配置中。旧版状态与密钥会在首次运行时迁移。

软件更新页面访问本项目的 GitHub Releases，列出可用新版本；经确认后校验官方安装包的大小和 SHA-256，并使用原生安装机制升级。Windows 需要 UAC 授权，Linux 原生包使用受限的 systemd 更新任务和包管理器。更新会停止工作台及实例，源码运行版本不支持自动安装。
