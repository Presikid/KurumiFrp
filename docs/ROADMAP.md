# Product roadmap

## 0.8 当前状态

主分支默认使用同进程的 frp 实现，工作台、账号与实例授权、远程节点启停、通知、规则批量操作和部分协议设置已分批开发。内置核心仍有未覆盖的底层异常路径；协议互通、多实例、异常清理和安装升级尚无完整验收证据，0.8 尚未发布。不要把源码已提交等同于运行验收完成。

## 核心任务剩余范围

- 继续补齐内置核心的隔离、资源回收及各协议功能，并明确无法保证的异常边界。
- 完成可靠的流量配额与更灵活的自定义权限；目前本机实例可分别授权查看、操作和日志，远程实例可分别授权查看和操作，配置详情、流量查看及操作员的启动、停止、重启、本机校验可单独限制。每日流量历史仅为可能不完整的采样增量，不能用于配额。
- 服务端控制入口已加入实例级 IP/CIDR 允许或阻止规则；Linux 的限定端口 nftables 来源过滤可选按 AS 或 HTTPS 清单刷新，重启后需手动重新应用。仍需补齐过滤规则生命周期、提供商自动发现、单点登录与 Passkey 等功能。
- 补齐参考运营面板的节点注册和上报、用户组与套餐、订阅和支付等能力；相关管理与业务数据需要明确各自的权限和持久化边界。
- 继续完善通知、登录安全、插件扩展以及界面和素材；尚未实现的功能不能只添加入口或以占位数据表示完成。

以下历史路线保留早期版本的设计背景；其中外部进程、自动化验证和版本规划不代表当前 0.8 默认实现或当前用户要求。

## Delivered foundation
Local instances and rules, encrypted persistence, conservative validation, versioned
drafts, official verify + owned-process lifecycle, responsive Chinese workbench.

## Next acceptance gates
1. Complete real frp 0.72 Linux roundtrip CI, Windows native lifecycle and real-host
   shutdown/crash recovery evidence. Verify HTTP/HTTPS/STCP data paths as well as TCP.
2. Capture proxy registration and target health separately from process state;
   surface frpc/frps status without exposing their management credentials.
3. Candidate/apply journal with last-known-good running versions and explicit,
   evidence-based rollback. Add diff views before applying.
4. Safely import existing TOML/include/Store configurations with explicit ownership;
   never silently overwrite a user's external configuration.
5. Windows service/tray and Linux systemd packaging, architecture-aware signed or
   checksum-verified releases, bounded restart policies and operator-controlled updates.

## Later
Remote enrollment and agent auth; device/instance/proxy/visitor topology; persistent
traffic accounting and quota enforcement; scheduling; secure complete backup/restore.
Monthly quota and schedules need a durable policy engine and are not frp UI labels.

No server deployment, existing MCP installation cleanup, DNS change, production
service change or license selection is authorized by this repository scaffold.


## 0.2 更新

已加入官方核心下载/校验/版本切换、用户级安装器、四平台预编译包、短时浏览器登录票据和本机自动端口选择。
安装器提供工作台自启动入口；实例也可逐个选择在工作台启动时自动恢复，启动前执行配置校验并显示失败原因。下载进度是真实字节数，不是模拟。


## 0.3 更新

已加入分发文件清单、内容验证的安装包自动清理、公开仓库在线安装脚本，以及 Windows/Linux 当前用户清洁卸载。原安装、进程停止及残留检查由 CI 验证。卸载的清理范围是本安装拥有的应用文件和系统集成项，不包含系统或浏览器取证记录。

## 0.4 原生安装

发行目标改为 Windows Setup.exe 和 Linux DEB/RPM；旧脚本/便携式发布流程停止，旧 tag 保留。后续仍需原生 ARM64 验证、签名发行、APT/YUM 软件源和旧版数据迁移向导。
