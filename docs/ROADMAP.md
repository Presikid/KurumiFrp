# Product roadmap

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
安装器提供的是工作台自启动入口；frpc/frps 实例的期望状态自动恢复仍未实现。下载进度是真实字节数，不是模拟。


## 0.3 更新

已加入分发文件清单、内容验证的安装包自动清理、公开仓库在线安装脚本，以及 Windows/Linux 当前用户清洁卸载。原安装、进程停止及残留检查由 CI 验证。卸载的清理范围是本安装拥有的应用文件和系统集成项，不包含系统或浏览器取证记录。

## 0.4 原生安装

发行目标改为 Windows Setup.exe 和 Linux DEB/RPM；旧脚本/便携式发布流程停止，旧 tag 保留。后续仍需原生 ARM64 验证、签名发行、APT/YUM 软件源和旧版数据迁移向导。
