# KurumiFrp 项目开发与 AI 协作规范

本仓库为 **KurumiFrp (KFRP)** —— 以时崎狂三（Tokisaki Kurumi）为主题的跨平台轻量级 FRP WebUI 管理器。任何参与本仓库维护或提交 PR 的 AI 代理与协作者必须严格遵守以下边界：

## 1. 核心产品定位与红线
- **纯 WebUI 轻量交互**：所有功能均在浏览器单一 WebUI 内完成；Windows 与 Linux 端均不添加系统托盘（Systray）或原生内嵌窗口（WebView）。
- **单管理员账号密码认证**：全局仅设唯一管理员账户（`用户名 + 密码` 登录与修改）。严禁引入多租户账户、RBAC 角色权限、流量计费账单或 OIDC 单点登录。
- **坚持随包内置官方原版 FRP 核心**：随安装包预置并调度 `fatedier/frp` 官方原版 `frpc` 与 `frps` 二进制，100% 兼容官方 TOML 全参数；严禁自研穿透协议或塞入阉割版 `third_party/frp` 源码分支。
- **严格围绕三大核心版块**：
  1. **转发端（Server / `frps`）**：总参数管理、多套独立配置文件切换、连接看板（实时流量统计、限流、限速、限时、开关）。
  2. **客户端（Client / `frpc`）**：多套独立配置文件从零构建、全类型穿透/P2P 打洞/官方插件配置与无感热重载。
  3. **应用控制（App Control）**：软件目录内（`data/configs/`）配置文件选择/导入/导出、唯一管理员用户名与密码设置、脱敏日志查看与核心版本状态。
- **严禁过度工程化与垃圾文档**：禁止添加宿主机防火墙（`nftables`/ASN 封禁）、跨机远程节点上报 Agent、Webhook 模板引擎、隧道数字签名等无关功能；禁止在 `docs/` 下堆砌碎片化说明文档。

## 2. 视觉素材与版权红线
- **保留狂三素材资源**：未经仓库所有者明确要求，严禁删除 `web/assets/`、`assets/themes/` 与 `assets/material/` 中的时崎狂三立绘和主题资源。
- **专有版权保留（No License）**：本项目不采用任何开源许可证（All Rights Reserved），严禁在仓库根目录新增 MIT / Apache / GPL 等开源 `LICENSE` 文件。
- **工具禁用**：禁止使用任何与 `cf聚合`（含 `mcpio`）相关的工具、插件、连接器或组件。

## 3. 提交与质量校验
- 每次提交 PR 前必须执行并通过 `gofmt -s -w .`、`go vet ./...` 与 `go test -race ./...`。
- **标准三段式语义化版本号（SemVer）**：版本号统一维护在根目录 `VERSION` 与 `internal/version/version.go` 中，严格遵循 `MAJOR.MINOR.PATCH` 标准三段式（如 `0.9.0`、`0.9.1`、`1.0.0`），严禁使用 `x.y.z.w` 多段式版本号。
