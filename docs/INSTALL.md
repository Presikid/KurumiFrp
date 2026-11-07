# KurumiFRP 0.3 安装与清洁卸载

程序无需 Go、Python、Node.js 或 Docker。Windows 安装及卸载使用系统 PowerShell；Linux 使用 `/bin/sh` 和常用系统命令。仓库现为公开仓库，官方下载不需要 GitHub Token。

## Windows 10/11：在线一条命令

在 PowerShell 中执行：

```powershell
& ([scriptblock]::Create((Invoke-WebRequest -UseBasicParsing 'https://github.com/Presikid/KurumiFrp/releases/download/v0.3.0/install-windows.ps1').Content))
```

这条命令执行本仓库的发布脚本；请只使用可信的官方地址。它识别 amd64/arm64，下载对应 ZIP 和 checksums.txt，校验后安装。安装成功且本机 API 验证通过后，删除本次下载的 ZIP、解压文件和临时目录。失败会保留下载目录，并输出位置，不冒充成功。

也可以从 Releases 下载 ZIP、解压后双击 `Install.cmd`。程序安装到 `%LOCALAPPDATA%\KurumiFRP`，创建桌面、开始菜单快捷方式，以及用户登录启动项（`-NoAutostart` 可不新增启动项）。它不是管理员级系统服务，安装包没有商业代码签名，不绕过系统安全拦截。

## Linux：在线一条命令

```sh
curl --fail --show-error --location --proto '=https' --proto-redir '=https' https://github.com/Presikid/KurumiFrp/releases/download/v0.3.0/install-linux.sh | sh
```

需要系统已有 `curl`、`tar`、`sha256sum`、`mktemp` 等命令；无需 Python/Go/gh。下载过程会校验安装包，验证工作台启动成功后清理本次下载和解压的临时目录。

离线安装：解压对应平台安装包，执行 `sh KurumiFRP/install.sh`。安装到 `~/.local/share/kurumifrp`，创建 `~/.local/bin/kurumifrp`。有可用的用户级 systemd 时启用用户服务，否则后台运行；不修改系统服务、防火墙或网站，不开启 linger。

## 安装包自动删除的准确范围

0.3 起，安装器只有在复制、配置和本机认证 API 检查成功后才清理来源。`PACKAGE-MANIFEST.json` 记录分发文件及 SHA-256；清理程序验证全部已知文件，再逐个删除，不对解压目录盲目递归删除。放在其中的额外个人文件会保留，非空父目录也保留。

在线安装器准确记录原始 ZIP/tar.gz 路径，因此可完整清理本次临时下载。手动安装会核对解压目录旁的标准名称压缩包，以及 Windows 默认“全部解压”目录上一层的标准名称压缩包。只有压缩包内部所有成员和本次分发清单一致、没有额外文件或链接时才删除；不能仅凭相同文件名删除。

手动移动、改名或另存的压缩包无法保证自动定位；发现多个匹配副本也不会猜测。不会扫描整个 Downloads 或磁盘。需要明确指定来源时，Windows 使用 `install.ps1 -ArchivePath '完整ZIP路径'`；Linux 使用 `KURUMI_INSTALL_ARCHIVE='/完整路径/安装包.tar.gz' sh KurumiFRP/install.sh`。显式指定但不匹配的包会保留并报错。

## 完整卸载

**会永久删除配置、认证令牌、本机主密钥、所有已下载核心及应用日志。需要保留的个人备份请在卸载前另行导出。**

Windows：在 **设置 → 应用 → 已安装的应用 → KurumiFRP → 卸载**，或运行 `%LOCALAPPDATA%\KurumiFRP\Uninstall.cmd`。输入 `UNINSTALL` 确认。安装器注册的是 HKCU 当前用户卸载项，不需要管理员权限。

Linux：

```sh
"$HOME/.local/bin/kurumifrp-uninstall"
```

输入 `UNINSTALL` 确认；自动化使用 `--yes`。在 HOME 未改变的情况下也可以运行 `sh "$HOME/.local/share/kurumifrp/uninstall.sh"`。

卸载器确认本次安装的所有权，阻止启动竞争，通过当前安装的本机令牌请求退出，等待工作台和其托管 frp 子进程停止，之后移除程序文件、data、cores、日志、运行配置、快捷方式/启动项、用户 systemd 配置及其启用链接、Windows 当前用户卸载注册项，最后移除自身与安装目录。

不会按 `frpc.exe` 等进程名批量强杀，不删除其他安装实例或手动管理的 frp。停止结果无法确认、遇到未知的旧锁、快捷方式被换成其他程序或文件占用时，报告未完成并保留可重试的信息。符号链接/junction 的外部目标不递归删除。

## “清洁”不等于操作系统取证无痕

本功能清理本安装创建并管理的应用文件和集成项，不扫描或擦除 Windows 事件日志、Prefetch、系统崩溃报告、systemd journal、浏览器历史/下载记录、系统还原点、备份或用户另存的导出。也不是安全擦除工具，不能承诺 SSD/文件系统恢复不了数据。

浏览器中的旧登录 Cookie 和主题偏好属于浏览器存储，外部卸载进程无法可靠清理所有浏览器、配置文件和不同访问地址；可在卸载前退出登录，并在浏览器中清除对应本机站点的数据。不会清除其他 localhost 服务的 Cookie。

## 升级与首次使用

从 0.2 升级前先在 WebUI 退出程序并停止实例，再安装 0.3。同一默认安装目录保留配置、令牌和主密钥；原来自定义 `--data` 或 `--frp-dir` 的手动部署不被自动接管或清理。不要直接删除未知进程留下的 manager.lock。

打开「核心管理」选择官方 frp 版本 → 下载 → 启用，然后创建实例与规则。管理端仍只监听回环地址，自动选择 9000–9010 的空闲端口；无桌面的远端服务器通过 SSH 转发访问。核心下载和卸载都不要求开公网管理端口。

Windows/Linux amd64/arm64 发行包；ARM64 交叉编译不等于原生硬件验收。frp 实例的开机自动恢复仍未实现，工作台用户级启动入口不代表实例自启动。
