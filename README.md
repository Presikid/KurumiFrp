# KurumiFrp (KFRP)

**KurumiFrp** 是一款以 **时崎狂三（Tokisaki Kurumi · 刻刻帝 Zafkiel）** 为视觉主题的轻量级跨平台 **FRP（`frpc` / `frps`）双端 WebUI 管理器**。软件随安装包预置官方原版 [fatedier/frp](https://github.com/fatedier/frp) 双端核心，让你在一个纯粹、清爽的浏览器控制台中完成转发端与客户端的全部配置与连接管控。

## 核心特性

- **时崎狂三 · 刻刻帝主题纯 WebUI**：内置四套时崎狂三主题色板（绯夜黑金、侦探书房、月夜魔女、白昼礼装）与专属立绘界面；Windows 与 Linux 全程统一在单个浏览器 WebUI 中操作，无冗余托盘或原生弹窗负担。
- **内置官方原版 `frpc` + `frps` 双端核心**：100% 调用上游官方原版二进制，原生兼容全部标准 FRP TOML 配置参数，支持客户端隧道无感热重载（`reload`）。
- **三大直观功能版块**：
  1. **Ⅰ · 转发端 Server (`frps`)**：管理服务端总参数与多份互相独立的配置文件（A / B / C...），配备实时 **连接看板（Connection Dashboard）**，支持针对单条连接独立设置 **流量统计、限流、限速、限时与一键开关**。
  2. **Ⅱ · 客户端 Client (`frpc`)**：支持从零创建多套独立客户端配置，覆盖 `TCP` / `UDP` / `HTTP` / `HTTPS` / `STCP` / `XTCP`（P2P 打洞）及 `static_file`、`socks5` 等官方插件，实时反馈每条规则的远端贯通状态。
  3. **Ⅲ · 应用控制 App Control**：配置文件自包含存放在软件目录 `data/configs/` 内，支持一键选择切换配置方案、导入/导出 JSON 配置存档与官方 `.toml` 文件，集成脱敏实时日志、**软件本体一键静默自更新**与**官方 FRP 核心一键无感热更新**（支持可选本地下载代理）。
- **简易单用户账号保护**：采用唯一管理员「用户名 + 密码」登录验证（PBKDF2-HMAC-SHA256 加密存储），兼顾公网部署安全与个人使用便捷。
- **直装包与清洁卸载**：提供 Windows 安装程序（`Setup.exe`，支持自由选择任意盘符目录安装与一键清洁卸载）及 Linux 原生包（`.deb` / `.rpm`，自动配置 `systemd` 后台服务与开机自启）。

## 安装与使用

### 1. 安装包直装（推荐）

前往 [GitHub Releases](https://github.com/Presikid/KurumiFrp/releases) 下载对应系统安装包：

- **Windows（Win 7 / 10 / 11 / Windows Server）**：运行 `KurumiFRP-<版本>-Setup.exe`，可自由选择安装目录并按需勾选开机自启；安装完成后通过桌面或开始菜单快捷方式即可在浏览器打开 WebUI。卸载时支持一键**清洁卸载**，彻底移除软件目录内的所有配置文件与运行数据。
- **Linux（Ubuntu / Debian / Fedora / RHEL 等）**：
  ```bash
  # Debian / Ubuntu
  sudo apt install ./kurumifrp_<版本>_amd64.deb

  # RHEL / Rocky / Fedora
  sudo rpm -Uvh ./kurumifrp-<版本>-1.x86_64.rpm
  ```
  安装后 `kurumifrp.service` 自动在后台运行并开机自启，默认监听 `9000` 端口；执行 `sudo apt purge kurumifrp` 可彻底清洁卸载。

### 2. 源码构建与运行

本项目基于 Go 标准库构建（零第三方重型依赖）：

```bash
go build -o kurumi ./cmd/kurumi
./kurumi -listen 127.0.0.1:9000 -data ./data -open
```

若从源码直接运行，请将官方 `frpc` 与 `frps` 可执行文件放入 `./bundled/` 或 `./data/bin/` 目录（或加入系统 `PATH`）。

### 3. 初始登录说明

- 默认管理员用户名为：`admin`
- 首次全新启动时默认密码为空，登录进入控制台后请立即前往 **「Ⅲ · 应用控制」** 设置你专属的管理员用户名与登录密码。

## 版权声明（Copyright & No License）

Copyright © Presikid. **All Rights Reserved（保留所有权利）**。

本项目源码、界面设计与发行包**未授予任何开源许可证（No License）**。未经作者明确书面授权，禁止任何形式的商业分发、二次打包售卖、修改后再分发或衍生授权。安装包内随附的上游官方 [fatedier/frp](https://github.com/fatedier/frp) 二进制组件遵循其原有的 Apache License 2.0 许可声明。
