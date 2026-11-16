<div align="center">

<img src="web/assets/k_51.webp" width="108" height="108" style="border-radius: 22px;" alt="KurumiFrp Avatar" />

# KurumiFrp

**全量原生 FRP v1 可视化控制台 · 基于连接关系的跨实例远控与 P2P 编排中心**

[![Release](https://img.shields.io/github/v/release/Presikid/KurumiFrp?style=flat-square&color=e5487b&label=Release)](https://github.com/Presikid/KurumiFrp/releases)
[![CI Status](https://img.shields.io/github/actions/workflow/status/Presikid/KurumiFrp/ci.yml?branch=main&style=flat-square&label=Build%20%26%20Tests)](https://github.com/Presikid/KurumiFrp/actions)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20Docker-111636?style=flat-square)](#快速安装与启动)
[![Core](https://img.shields.io/badge/Bundled%20Core-Official%20fatedier%2Ffrp%20v1-ff7aa8?style=flat-square)](https://github.com/fatedier/frp)

</div>

<p align="center">
  <img src="web/assets/k_02.webp" width="88%" alt="KurumiFrp Stage Banner" />
</p>

**KurumiFrp** 是一款围绕时崎狂三主题打造的桌面级与服务器级内网穿透管理软件。它直接内置并调度上游官方 [`fatedier/frp`](https://github.com/fatedier/frp) 原生核心，将 `frps` 服务端与 `frpc` 客户端拆分为清晰独立的双工作区，不仅**在图形界面上 100% 原生覆盖官方配置文件支持的全部功能与插件**（同时支持 UI 表单与 `.toml` 代码双向实时编辑），更在官方 FRP 之上实现了**基于真实实例连接的 `Server → Client` 跨网络远控与 `XTCP` P2P 协同编排**。

## 核心特性一览

| 模块维度 | 能力详情 |
| :--- | :--- |
| **分板块全量原生 UI 控制** | 独立划分 **`FRPS 服务端`** 与 **`FRPC 客户端`** 板块，从 `TCP / UDP / HTTP / HTTPS / TCPMUX / STCP / SUDP / XTCP` 全协议、端口段批量映射（`parseNumberRangePair`）、全部 **10 种内置客户端插件**（`static_file`、`socks5`、`https2http`、`tls2raw`、`unix_domain_socket`、`virtual_net` 等），到 `OIDC/Token` 鉴权、`QUIC/KCP/WSS`、`SSH 隧道网关`、负载均衡与健康检查，全部可通过 UI 控件原生配置。 |
| **UI ⇄ TOML 双向实时同步** | 修改左侧任意可视化表单或隧道卡片时，右侧标准 `frps.toml` / `frpc.toml` 代码编辑器毫秒级实时刷新；反过来直接在代码框手改 TOML，也能一键反向解析回填至全部 UI 表单并触发 `frpc reload` 零中断热重载。 |
| **基于连接的跨实例远控** | 权限严格由实例间的真实连接关系决定（而非本地权限组）：任一 `frpc` 实例连上某台 `frps` 实例后，该 `frps` 即可通过加密反向控制通道直接查看、修改并热重载该 `frpc` 的配置，无需在 NAT 客户端或服务端额外开放任何端口；而每个 `frpc` 实例在本地仅能管理自身配置。 |
| **跨无公网节点 P2P 协同** | 内置 `XTCP / STCP / SUDP` 协同向导，支持服务端管理员直接远控下发被访问端规则、一键双端打通 P2P，或生成对端 `[[visitors]]` 配置片段供朋友在本地 WebUI 一键导入，支持 `STCP` 自动保底回退（`fallbackTo`）。 |
| **服务端连接管控看板** | 实时采集每条映射隧道的入/出站累计流量、实时速率与在线连接数，支持按隧道独立设置**总开关、流量上限（MB）、速率阈值（KB/s）与到期自动阻断时间**。 |
| **零 UAC 热更新与多方案存储** | 支持配置本地 HTTP/SOCKS5 代理（及自动识别系统代理），一键经官方 SHA-256 校验热替换 `fatedier/frp` 核心或静默自更新 `KurumiFrp` 本体；内置多套 `data/configs/*.json` 独立工作区方案切换与自动密钥日志脱敏。 |

## 连接驱动的五节点组网实例（A / B / C / D / E）

<p align="center">
  <img src="web/assets/k_41.webp" width="76%" alt="KurumiFrp Connection Topology" />
</p>

假设你和朋友拥有以下五台处于不同网络环境的设备，均安装了 **KurumiFrp**：

1. **云服务器 A（有公网 IP）**：在本地新建并启动一个 `frps` 实例。
2. **学校电脑 B（校园网无公网 IP）**：在本地新建一个 `frpc` 实例连接云服务器 **A**。放学回家后，你在自己电脑浏览器登录 **A 的 WebUI**，由于 A 的 `frps` 已与 B 的 `frpc` 建立连接，你可以直接在 A 的面板中远控 B 的配置，将 **B 的 `7777` 端口映射到 A 的 `9999` 端口**并立即热重载生效。
3. **家里电脑 C（家宽无公网 IP）**：在本地建立一个 `frpc` 实例连接云服务器 **A**。
4. **公司电脑 D（有公网 IP，混合双实例）**：在本地同时建立一个独立的 `frps` 实例（等待服务公司侧其他用户）和一个 `frpc` 实例（连上云服务器 **A**，将自身 `6768` 端口通过 UDP 映射到 A 的 `10000` 端口）。此时 **A 仅能远控 D 连过来的这一个 `frpc` 实例**，绝不会触碰 D 自己独立运行的 `frps` 实例。
5. **朋友家电脑 E（家宽无公网 IP）**：在本地建立一个 `frpc` 实例连接云服务器 **A**。随后朋友访问 **E 的本地 WebUI** 发起建立 P2P 连接，而你通过 **A 的 WebUI** 直接远控 **C 的配置文件**响应配对，**C 与 E 即可经由 A 协调打洞成功建立点对点直连隧道**。

## 视觉与布局设计

界面布局致敬清爽的画廊与编辑部排版风格：
- **HOME 全屏轮播主舞台**：11 张 `16:9` 横版时崎狂三场景插画无缝渐变轮播，左下胶囊切换与胶片抽屉，右下快捷工作区直达。
- **结构化双栏工作台与原画资源库**：细网格浅底搭配羽化横幅、左侧常驻目录树、右侧 `3:4` 白边编号立绘卡片与手风琴参数面板；二进制内嵌轻量 WebP 画廊，同时在仓库 `assets/material/webui_v094/` 独立归档全部 35 张原画质大图与 `manifest.json` 校验清单（不增加编译二进制体积，支持后续扩展在线原画资源包升级）。
- **严密登录保护**：首次运行默认管理员账号为 `admin`（初始密码为空），未通过身份认证前不暴露任何面板控件与实例接口。

## 快速安装与启动

前往 [**GitHub Releases**](https://github.com/Presikid/KurumiFrp/releases) 下载对应系统的原生安装包（所有发行包均已内置官方 `frps` 与 `frpc` 二进制文件，开箱即用）：

### Windows 桌面与服务器 (`amd64` / `arm64`)
下载并运行 **`KurumiFRP-0.9.4-Setup.exe`**：
- 支持自定义任意安装目录，双击桌面快捷方式自动在默认浏览器打开本地控制台。
- 支持静默命令行参数：
  ```powershell
  kurumi.exe -listen 0.0.0.0:9000 -headless
  ```

### Linux 系统服务与便携包 (`deb` / `rpm` / `tar.gz`)
```bash
# Debian / Ubuntu 安装并启动 systemd 服务
sudo apt install ./kurumifrp_0.9.4_amd64.deb
sudo systemctl enable --now kurumifrp

# 查看初始监听地址或本地维护令牌
sudo kurumifrp -paths
```

### 从源码构建
需要 Go `1.23+` 环境：
```bash
go test -race ./...
go build -trimpath -ldflags="-s -w" -o kurumifrp ./cmd/kurumi
./kurumifrp -listen 127.0.0.1:9000 -open
```

## 版权与第三方组件声明

- **KurumiFrp** 源码、架构设计与视觉资源由 **Presikid** 独立维护，**保留所有权利（Copyright © Presikid. All Rights Reserved.）**，未经明确书面授权不授予任何开源再分发或商业使用许可。
- 发行安装包内随附的上游官方 `frpc` / `frps` 可执行组件来自 [`fatedier/frp`](https://github.com/fatedier/frp)，遵循其原始 **Apache License 2.0** 协议。


### 双档视觉画质架构（v0.9.6+）
- **默认轻量内置**：二进制仅内嵌约 `2.4 MB` 高压缩比 WebP 素材，确保各平台安装包小巧轻量。
- **按需拉取原画包**：登录 WebUI 后可在「应用与画廊」一键从 GitHub 仓库并发拉取 `assets/material/` 原画质素材包（内置代理复用、SHA-256 校验、断点续传与零重启热切换）。
