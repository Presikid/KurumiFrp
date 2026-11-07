# 0.5 验证范围

新增 Windows Known Folder 路径、防环境变量劫持、NTFS 账户目录权限、临时会话清理与保留未知文件的单元测试。Windows 原生测试实际安装到默认 Program Files，检查 HKLM 卸载项，创建临时普通账户并验证可运行、不能读取另一账户的密钥或写入 Program Files；测试子 frp 使用受控 TEMP，检查升级和跨账户卸载。Linux 原生测试检查子进程 TMPDIR 与 systemd RuntimeDirectory 生命周期。

新增测试是否通过应查对应提交 CI，不以此文档代替结果；没有做全系统文件写入跟踪、所有 Windows 客户端版本或 ARM64 实机验证。下文为历史版本记录。

# Validation evidence for 0.1.0 foundation

Performed in the authoring sandbox, 2026-11-07:
- `go test -race ./...`: 11 Go test functions pass (including subprocess lifecycle).
- `go vet ./...`: pass.
- `go build ./cmd/kurumi`: Linux amd64 build passes.
- `GOOS=windows GOARCH=amd64 go build`: cross-compilation passes; not a native run.
- `GOOS=linux GOARCH=arm64 go build`: cross-compilation passes; not a native run.
- `node --check web/app.js`: pass.
- Offline Chromium UI harness at 1440px and 390px: login, instance and rule creation,
  rule edit, duplicate remote-port rejection, redacted TOML, dark/light views,
  saved-state reads and no document-level mobile overflow pass. No page errors.

The sandbox Chromium administrator policy blocked navigation to loopback URLs.
The UI run therefore used `KURUMI_OFFLINE_UI=1`: real DOM/rendering, with fetch bridged
by the test harness to a real disposable local Go server. It is NOT proof of actual
browser transport, cookie enforcement or a normal reload session. The Go HTTP tests
separately cover authentication, Origin, Host and cookie logout behavior.

Network DNS to GitHub was unavailable in the sandbox, so an official frp binary
could not be downloaded there. Real frp verification and TCP forwarding are NOT
locally certified. `scripts/frp-smoke.py` and the pinned Linux CI step provide that
acceptance check. Record actual CI results separately; do not call a workflow green
merely because the YAML exists. Windows actual execution is also a CI/host gate.

No user's machine, VPS, running frp instance, previous MCP stack or repository main
branch was used for these tests. Sandbox sample rules are not seeded in the product.

## 0.2 本次验证

新增核心单元测试覆盖：官方元数据筛选、目标平台、缺少摘要、下载任务、并发与取消、SHA-256 不符、ZIP/tar 路径穿越/链接/重复/超大/缺失、真实格式的测试二进制、启用探测、篡改拒绝、活动指针保留。测试用 HTTP transport 和程序自身构造归档，不冒充真实 frp 网络下载。

新增 API 测试覆盖认证、运行中拒绝启用、一次性票据重放与过期、退出程序权限。新增安装 smoke 在临时 HOME/用户目录两次执行安装、启动真实本机 API、换票据、退出并比较密钥不变。

本地 Linux Go 单测、race、vet，四目标交叉编译，Linux 安装 smoke 已通过。浏览器页面在此沙箱使用离线 DOM + 真实临时 API 桥接；核心目录是测试 fixture，验证选择、错误平台禁装、主题及手机宽度，不声称是实际公网目录。真正浏览器 localhost 测试、Windows 原生安装和官方核心下载/转发由 CI 执行，以对应提交的 Actions 结论为准。

`core-smoke.py` 通过实际认证 API 下载固定官方版本，校验/提取/启用，再调用 frp-smoke.py 验证 TCP 字节往返。只有所有 CI 测试成功，发布 job 才能创建四平台预编译包及 checksums。ARM64 是交叉编译结果，不是 ARM64 原生运行验收。


## 0.3 验证

新增 Go 测试覆盖：已知安装文件清理、保留用户额外文件、源文件修改拒绝、恶意清单路径拒绝、自删除源可执行文件拒绝、符号链接拒绝、ZIP/tar.gz 全成员匹配与额外文件拒绝、本机维护认证和失败时保留状态。

升级安装 smoke：在隔离用户目录重复安装；确认原始压缩包及解压载荷删除、个人文件保留、令牌/主密钥不变；人为制造退出认证失败确认安装被保留；启动真实的合成 Go 子进程作为 frps 生命周期 fixture，完整卸载后检查管理器/子进程退出、数据与集成项删除、外部符号链接/junction 目标未受损。

本地已运行 Linux 安装与卸载 smoke、Go 单元测试和各包竞态检测。Linux 在线安装脚本的传输采用受控 fixture，验证临时下载目录清理，不冒充公网下载。Windows 原生安装/卸载与 junction 测试、真实官方 frp 下载与 TCP 转发由提交的 GitHub Actions 执行；结果以对应 run 为准。ARM64 仍为交叉编译，不声称实机验证。

## 0.4 原生安装验证

沙箱已执行 Go 测试、vet、真实 dpkg 构建/安装/重装/remove/purge 与本机认证 API，未使用用户设备。沙箱没有活动 systemd，因此沙箱通过不等于系统启动验证。Windows Setup.exe 由 CI 的 Inno Setup 编译并通过真实安装引擎测试；Linux CI 增加真实 systemd + dpkg 生命周期和 Fedora 容器 RPM 测试。发布 job 只有两个平台全部检查成功后才能上传 Setup.exe、两个 DEB、两个 RPM 和 checksums.txt。以该提交的 Actions 结果为准，不以工作流文件存在代替成功。


## Local target diagnostic checks

The target diagnostic tests cover TCP reachability, HTTP virtual-host Host handling,
HTTP application error responses, HTTPS certificate verification, UDP's unsupported
state, invalid ports, authenticated API access, stale revisions, and proof that a
slow target check does not hold the manager-wide application mutex. The browser
smoke test probes the disposable local manager listener and verifies that the
connection table renders a successful result. These checks validate only the local
target diagnostic behavior; they do not claim remote frp registration or Internet
reachability.
