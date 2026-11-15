## KurumiFrp v0.9.4 更新说明

这一版彻底推倒重做了整套 WebUI 界面与底层控制架构，重点解决了以往面板「只能配几个基础端口、很多官方 FRP 功能还得手搓文件」以及「客户端躲在校园网/家宽 NAT 后面没法在云端统一调整」这两个核心痛点。

### 1. 全新画廊级 WebUI 布局与精选视觉系统
- **重构整体界面架构**：采用顶部深靛蓝导航栏 + 全屏无界轮播主舞台（`HOME`）+ 清爽细网格双栏工作区（左侧固定实例/分类目录树 + 右侧白边编号立绘卡片流）的布局体系，去除冗余花哨粒子与杂乱装饰徽章。
- **内置 35 张精筛时崎狂三插画 + 仓库归档原画质资源库**：二进制内嵌轻量 WebP 素材保障启动速度，同时在仓库 `assets/material/webui_v094/` 目录按比例分类归档全部 35 张未经重压缩的原画质大图、原尺寸透明贴纸与 `SHA-256` 清单（`manifest.json`），为后续「在线下载原画资源包升级画质」预留完整接口。
- **严格未登录隔离门禁**：未登录状态下仅加载登录验证页，所有实例状态与配置 API 均返回 `401` 拦截，杜绝未授权预览或误操作。

### 2. 基于真实连接的跨实例远控与 P2P 协同（`Server → Client`）
- **实例级连接控制平面**：当任意 `frpc` 客户端实例连接到 `KurumiFrp` 托管的 `frps` 服务端实例后，会自动基于该连接建立加密反向控制通道（无需在云服务器或内网机器上额外开放任何公网端口）。
- **连接决定管理边界**：
  - 每个 **`frps` 服务端实例**可以直接查看、编辑、热重载所有已连入它名下的 **`frpc` 客户端实例**配置（例如放学回家打开云服务器 A 的面板，就能直接给学校电脑 B 新增 `7777 → 9999` 映射；同一台机器 D 同时跑 `frps` 与 `frpc` 时，上游 A 也只能管理 D 连过来的那个 `frpc` 实例，绝不越权触碰 D 自己的 `frps`）。
  - 每个 **`frpc` 客户端实例**在本地仅能管理自身配置，也可随时在本地开关是否允许上游服务端远控。
- **跨客户端 XTCP P2P 协同向导**：支持在服务端面板一键为内网节点（如家里电脑 C 与朋友电脑 E）配对下发 `XTCP` 点对点打洞规则及 `STCP` 自动回退保底通道，也支持一键导出对端 `[[visitors]]` 配置片段供朋友在本地面板直接导入。

### 3. `frps` 与 `frpc` 分板块 100% 原生 UI 覆盖 + 双向实时 TOML 同步
- **`FRPS` 服务端全参数 UI 化**：原生覆盖 `bindAddr/Port`、`KCP/QUIC`、`Vhost HTTP/HTTPS/Timeout`、`TCPMux HTTP CONNECT`、`Token / TokenSource 文件 / OIDC` 鉴权与 `additionalScopes`、连接池与 `QUIC/TLS` 双向证书、`SSH 隧道网关 (sshTunnelGateway)`、`allowPorts` 端口白名单、自定义 `[[httpPlugins]]` 及连接流量/限流/限速/限时/开关看板。
- **`FRPC` 客户端与隧道全参数 UI 化**：原生支持全部 **8 种隧道协议**（`tcp / udp / http / https / tcpmux / stcp / sudp / xtcp`）、`parseNumberRangePair` **批量端口段映射**、全部 **10 种官方客户端插件**（`static_file / http_proxy / socks5 / https2http / https2https / http2https / http2http / tls2raw / unix_domain_socket / virtual_net`）、上游 `transport.proxyURL` 连接代理、隧道级加密/压缩/带宽限速、`Proxy Protocol v1/v2`、负载均衡分组（`loadBalancer`）、本地健康检查（`healthCheck`）、Header 改写及 `XTCP Visitor` 全套打洞与自动回退参数。
- **UI 表单 ⇄ `TOML` 代码双向实时联动**：右侧常驻当前实例完整的标准 `frps.toml` / `frpc.toml` 代码视图。调整任意 UI 控件会即时刷新代码；直接修改代码也可一键反向解析回填至全部 UI 表单与隧道卡片，保存后自动触发零中断热重载。

### 4. 安装与升级说明
- **Windows 用户**：可在旧版面板「应用控制 → 更新」直接一键自更新，或下载下方 `KurumiFRP-0.9.4-Setup.exe` 覆盖安装（原有配置方案与凭据自动保留）。
- **Linux 用户**：提供原生 `deb` / `rpm` 安装包（含 `systemd` 服务）与免安装便携压缩包 `tar.gz`，随包内置通过官方 SHA-256 校验的上游 `fatedier/frp` 核心。
