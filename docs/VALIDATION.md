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
