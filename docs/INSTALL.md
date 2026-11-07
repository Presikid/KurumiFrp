# KurumiFRP 0.4 原生安装

0.4 不再发行“解压后运行 Install.cmd/install.ps1/install.sh”的便携式安装包。
Windows 发行真正的 Inno Setup 图形化 EXE；Linux 发行由 dpkg/APT 或 RPM/DNF 管理的系统软件包。Python、Go、Inno Setup、rpmbuild 只用于构建/测试，用户无需安装。Windows 安装、启动和卸载均不再调用 PowerShell/CMD 脚本。Linux 软件包内部的维护脚本由包管理器调用，这是包生命周期机制，不是用户安装器。

## Windows 10/11

下载 Releases 中的 **KurumiFRP-0.4.0-Setup.exe**，双击进入安装向导。一个安装文件自动选择 x64 或 ARM64 主程序，不需要先解压或运行命令。

安装向导提供安装位置、桌面快捷方式、用户登录启动项、成功后删除 Setup.exe 等选项。默认安装到当前用户的 `%LOCALAPPDATA%\Programs\KurumiFRP`，无需管理员权限。属于正式注册的当前用户应用，不是全用户系统服务。快捷方式直接运行原生 `kurumi.exe`；数据与核心位于安装目录的 data/cores 子目录，不依赖当前工作目录。

安装完成后启动本机工作台并验证认证 API。验证成功才执行安装包清理：关闭安装向导后，已安装程序等待下载的 Setup.exe 解锁，重新核对其 SHA-256，只删除这个准确的文件，不扫描下载目录。文件被其他程序持续占用时会保留，不承诺绕过文件锁。

原生安装版覆盖升级会先安全停止工作台，保留数据、令牌和主密钥，再替换程序文件。卸载入口是 **Windows 设置 → 应用 → 已安装的应用 → KurumiFRP**，或开始菜单的卸载快捷方式。卸载会明确提示删除全部配置、密钥和核心；先停止托管进程，再清理运行数据，由安装引擎移除文件、快捷方式、注册项和自身。未知目录/未核实的停止结果不会强行清理。

尚未购买 Authenticode 代码签名证书，安装包可能显示“未知发布者”或 SmartScreen 提示，不应把有 EXE 后缀理解为已签名。

## Debian / Ubuntu

按架构下载 `kurumifrp_0.4.0_amd64.deb` 或 `kurumifrp_0.4.0_arm64.deb`，使用系统软件安装器打开，或执行：

```sh
sudo apt install ./kurumifrp_0.4.0_amd64.deb
```

升级同样使用 `apt install ./新版本.deb`，由包管理器处理升级，而不是手动复制文件。

```sh
sudo apt remove kurumifrp   # 移除程序，保留配置与密钥
sudo apt purge kurumifrp    # 清除程序及全部托管配置、密钥、核心和服务账户
```

## Fedora / RHEL 系 RPM 发行版

下载 `kurumifrp-0.4.0-1.x86_64.rpm` 或 `kurumifrp-0.4.0-1.aarch64.rpm`：

```sh
sudo dnf install ./kurumifrp-0.4.0-1.x86_64.rpm
sudo dnf remove kurumifrp
```

RPM 升级保留数据；最终卸载会清理本安装管理的状态、密钥、核心及专用账户，卸载前需要自行备份。

这些是上游独立发行的软件包，不表示已进入 Debian/Fedora 官方仓库。没有配置 APT/YUM 软件源，也没有发行仓库 GPG 签名；不要直接假定 `apt install kurumifrp` 可以联网找到软件。发布文件附 SHA-256，校验不等于独立发行签名。

## Linux 文件与服务布局

- `/usr/bin/kurumifrp`：包管理器拥有的原生程序。
- `/usr/lib/systemd/system/kurumifrp.service`：系统级服务单元。
- `/var/lib/kurumifrp/data`：配置、令牌、主密钥、历史和运行状态。
- `/var/lib/kurumifrp/cores`：下载的 frpc/frps。
- `/var/lib/kurumifrp-package`：root 专属的服务账户所有权记录，完整卸载时删除。
- `/usr/share/doc/kurumifrp` 与 man 手册：安装文档。

服务以专用非 root 账户运行，只监听回环地址 9000–9010，不修改现有网站或防火墙。桌面用户也通过浏览器访问这个本机服务，不会另起一套 HOME 内的便携实例。低权限服务不能直接监听 1024 以下的端口，管理员如需此能力应另行制定最小权限策略。

```sh
systemctl status kurumifrp
sudo systemctl start kurumifrp
sudo systemctl stop kurumifrp
sudo cat /var/lib/kurumifrp/data/listen.addr
sudo kurumifrp --show-token
```

没有活动 systemd 的容器会完成软件包登记，但不会谎称服务已经运行。远端服务器仍需 SSH 转发访问本机管理端口。systemd 启动工作台不等于自动启动保存的 frp 实例；后者仍未实现。

## Linux 安装包清理与标准包事务

DEB/RPM 不在维护脚本里猜测并删除用户下载的包，也不删除 APT/DNF 缓存或事务数据库。原始包路径归调用安装命令的用户管理；需要安装后删除时，可以把标准包命令和“启动验证后删除这个准确文件”合在一次执行中：

```sh
file=kurumifrp_0.4.0_amd64.deb
sudo apt install "./$file" && sudo kurumifrp --check-running && rm -- "./$file"
```

RPM 将其中安装命令改成 `sudo dnf install "./$file"` 并选择对应 `.rpm` 文件即可。安装或启动验证失败时保留原文件。手动双击 DEB/RPM 安装时是否清理原文件由系统安装器决定，不能承诺所有发行版都会自动删除。

## 从 0.3 脚本版迁移

0.4 不会把未知目录或旧用户级脚本安装当作新安装强行清除。Windows 默认安装目录变化；Linux 从 HOME 内用户实例改成 `/var/lib` 下系统服务。旧版本不会自动被接管。

需要迁移数据时，先在旧 WebUI 退出并停止实例，完整备份旧 `data` 和 `cores`（包括 access.token/master.key；脱敏导出不能替代备份），通过旧版卸载器移除旧启动项，再安装原生版。停止新程序，将备份放入新安装的 data/cores 目录；Linux 必须把它们的所有者设为 kurumifrp。不要同时运行两套管理器操作相同数据。

## 清洁卸载的边界

卸载清理本次安装拥有的应用文件、配置、核心和集成项，不清除操作系统审计日志、systemd journal、浏览器历史/站点存储、系统还原点、包管理器历史或用户另存的备份。不承诺取证无痕，也不删除其他 frp 安装。符号链接/junction 外部目标不递归删除。

Windows x64 和 Linux amd64 的原生安装测试由 CI 执行；ARM64 构建不等于 ARM64 硬件验收。Fedora 容器 RPM 测试也不能替代所有 RHEL/SELinux 环境验证。
