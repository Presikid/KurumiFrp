# KurumiFRP 0.4.0 原生安装预览版

- 一个真正的图形化 Setup.exe，自动选择 Windows x64/ARM64，不再是 PS1/CMD 安装入口。
- 安装向导、安装位置、原生快捷方式、可选登录启动、Windows 应用列表和原生卸载器。
- 安装及启动验证成功后清理本次 Setup.exe；卸载前验证停止托管进程并清除应用数据。
- Debian/Ubuntu DEB 和 Fedora/RHEL 系 RPM，支持 amd64/x86_64 与 arm64/aarch64。
- 包管理器文件登记、升级、移除/清除流程；专用低权限账户、systemd 系统服务、标准 /usr 与 /var/lib 布局。
- Linux 尊重包管理器语义；APT remove 保留状态，purge 完整清除。RPM 最终卸载清理状态，升级保留。
- 原有核心管理、实例、规则、草稿历史与日志功能保留；Windows 子进程不弹终端窗口。

本发布不提供旧便携 ZIP/tar.gz 或 install.ps1/install.sh。未签署 Authenticode 或发行版仓库 GPG 签名，尚未建立 APT/YUM 在线仓库。ARM64 为交叉编译；旧 0.3 的用户目录安装需要显式迁移，不能假设自动接管。系统/浏览器/包管理器历史不属于清洁卸载的擦除目标。
