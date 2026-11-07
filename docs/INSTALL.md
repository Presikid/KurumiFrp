# KurumiFRP 0.2 安装

不需要 Go、Node.js、Docker、管理员权限、证书或独立网站。

## Windows 10/11

登录有仓库权限的 GitHub 账户，在 Releases 下载与你机器匹配的 `windows_amd64.zip`（Intel/AMD）或 `windows_arm64.zip`（ARM64 Windows）。解压后双击 **Install.cmd**，即可安装到 `%LOCALAPPDATA%\KurumiFRP`，生成桌面/开始菜单快捷方式并打开本机面板。

默认创建当前用户登录时后台运行的启动项（不是系统服务）；不需要时删除启动文件夹中的 KurumiFRP 快捷方式。脚本也支持 `-NoAutostart`。安装包没有商业代码签名，不绕过 Windows 安全提示。

## Linux

下载 `linux_amd64.tar.gz`（Intel/AMD）或 `linux_arm64.tar.gz`（ARM64），解压后执行一条命令：

```sh
bash KurumiFRP/install.sh
```

安装到 `~/.local/share/kurumifrp`，生成 `~/.local/bin/kurumifrp` 启动入口。可用时注册当前用户 systemd 服务；否则以普通用户后台进程运行。不会安装系统级服务、开启 linger、改防火墙或占用公网监听。服务在用户会话环境下启动，不承诺无人登录时开机运行。

## 首次使用

快捷方式会打开 WebUI 并以有效期 60 秒的一次性凭据自动登录，不把长期 access.token 放到 URL。默认只监听本机，在 9000–9010 内选空闲端口并记住结果。无桌面环境的 Linux 会显示地址；远程服务器需要 SSH 转发，不要把管理接口裸露公网。

1. 打开「核心管理」，刷新官方稳定版本（支持 frp >= 0.60）。
2. 默认选中**运行主机**对应平台。点击「下载到本机」。
3. SHA-256、解压和二进制架构检查完成后，点击「启用此版本」。
4. 创建 frpc/frps 实例与规则，校验配置，然后启动。

下载不打断运行中的连接。启用/回退核心前必须停止全部实例；切换后不会自动启动实例。版本目录保留，不会直接覆盖原来的 frpc/frps。

## 私有仓库与一条远程安装命令

当前仓库为私有仓库，匿名 `curl https://raw.githubusercontent.com/...` 和公开 Release 下载链接**不能**作为有效安装入口。已登录 GitHub CLI 的 Linux 用户可用以下整段命令下载、解压并安装；无需修改源码或任何配置：

```sh
(
set -eu
case "$(uname -m)" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo 'Unsupported CPU'; exit 1;; esac
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
gh release download v0.2.0 --repo Presikid/KurumiFrp --pattern "KurumiFRP_0.2.0_linux_${arch}.tar.gz" --pattern checksums.txt --dir "$work"
(cd "$work" && grep "  KurumiFRP_0.2.0_linux_${arch}.tar.gz$" checksums.txt | sha256sum --check --strict)
tar -xzf "$work/KurumiFRP_0.2.0_linux_${arch}.tar.gz" -C "$work"
bash "$work/KurumiFRP/install.sh"
)
```

没有 GitHub CLI 时，在浏览器登录后下载安装包即可。不要把写仓库的 PAT 放进命令行、URL 或公开安装脚本。是否已发布该版本，以 Releases 页面为准。

## 升级、退出与数据

相同版本重复安装不会轮换密钥，正在运行时只重新打开面板。安装不同版本前，使用 WebUI 的「退出程序并停止实例」退出，再重新安装。不要在 frp 仍运行时删除 manager.lock。原 0.1 手动部署的数据目录不会被自动迁移到用户安装目录，请继续使用原 --data 参数，或在停机后完整备份/迁移原目录。

`access.token` 是备用登录凭据；数据目录还包含 `master.key`，二者都必须保密。核心下载没有 GitHub 私有仓库 PAT，仅读官方公开发布。不能访问 GitHub 时，为 KurumiFRP 进程设置 `HTTPS_PROXY` 后重启；不会静默使用第三方镜像或关闭 TLS 校验。

SHA-256 来自 GitHub 官方发布元数据，提供传输完整性检查，不是独立的软件发行签名。当前自动安装支持 Linux/Windows/macOS 的 amd64/arm64 资产；本项目安装包只发布 Linux/Windows 的 amd64/arm64。32 位、ARMv7、Windows 7 均不在本版程序安装器支持范围内。
