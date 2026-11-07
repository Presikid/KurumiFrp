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
