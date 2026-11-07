# KurumiFRP

A local-first frp workspace: Chinese WebUI, embedded Go server, saved drafts,
configuration checks, and **real locally owned frpc/frps processes**.

**0.1.0 foundation / preview.** Not the discontinued GitHub MCP hosting project.
No cloud account, OAuth gateway, Docker, DNS API or new TLS certificate is required.

## Run

Build using a supported Go toolchain (language baseline: Go 1.23):

```sh
go test ./...
go build -o kurumi ./cmd/kurumi
./kurumi --data ./data --frp-dir ./frp --listen 127.0.0.1:9000
```

In a second terminal, obtain the generated local access token:

```sh
./kurumi --data ./data --show-token
```

Open `http://127.0.0.1:9000`, paste the token, create an instance and add rules.
On Windows use `kurumi.exe`. Never send `access.token`, `master.key` or the data
directory to a chat or commit them to Git.

The manager runs without frp installed, but validation and start operations fail
explicitly until you put **trusted official frpc/frps binaries** in `./frp` (use
`frpc.exe` / `frps.exe` on Windows). The executable paths are operator-selected,
not supplied by a WebUI request. Do not put arbitrary programs in that directory.

Official frp release downloads: <https://github.com/fatedier/frp/releases>.
The integration test targets **frp 0.72.0** with a pinned archive SHA-256.

## Implemented

- Overview, connection board, instance management, configuration center, logs;
  responsive light/dark Chinese UI, no external assets or front-end build step.
- Multiple local frpc/frps instances. TCP, UDP, HTTP, HTTPS, STCP publisher and
  STCP visitor forms. Per-proxy bandwidth limits in **KB/s**, not Mbps.
- Saved drafts with revision conflict checks. Port/name/domain collision checks
  are conservative and scoped to known endpoints; DNS aliases cannot be proven equal.
- Official `frpc/frps verify -c` before launch. Restart validates the candidate
  **before** stopping the old process. No arbitrary shell execution endpoint.
- Owned child-process start/stop/restart; PID, exit, draft-vs-applied hash and
  bounded redacted output. No invented throughput or connection status.
- AES-256-GCM encrypted tokens in the state file; encrypted snapshots (last 30);
  redacted TOML and workspace exports. Restore requires all managed processes stopped.
- Loopback-only management, strict Host/Origin checks, HttpOnly SameSite cookies,
  login throttling, logout revocation, 12-hour sessions, CSP and request limits.

## Important boundaries

A live process is **not** evidence of proxy registration or end-to-end connectivity.
This UI says so explicitly. Saving/toggling rules changes the draft only; use the
instance's restart action to apply. Restart interrupts connections. A runtime
launch failure does not automatically restore the previous process/configuration.

This release deliberately does not implement remote agents, multi-device orchestration,
Store API/hot reload, monthly traffic quota, scheduling, automatic frp upgrades,
tray integration, Windows services, or automatic startup of instances. These are
tracked in [the roadmap](docs/ROADMAP.md), not simulated as functional controls.

The UI's JSON/TOML exports are **redacted documentation**, not restorable backups.
For recovery, stop the manager and back up the **whole private data directory**, including
`state.json` and `master.key`; safeguard `access.token` too. A lost encryption key
cannot be regenerated to decrypt saved secrets. Never edit the state file while running.

Normal manager exit stops owned children. After a crash, `manager.lock` intentionally
blocks a second manager: inspect surviving processes and stop/reconcile them before
removing the lock. No PID-only adoption or termination of unrelated processes.

The management listener rejects non-loopback binds. For another machine use an
SSH local forward, keeping the same port and browser Host. Reverse proxies, TLS
termination and public deployment are **not** configured automatically. User-data
file permissions are Unix modes; Windows deployments must additionally restrict
NTFS directory ACLs. This preview is not a multi-user security boundary.

Read [architecture and security](docs/ARCHITECTURE.md) before deployment.
No license has been selected by the repository owner; do not assume a distribution license.

## Tests

```sh
go vet ./...
go test -race ./...
python3 scripts/frp-smoke.py ./kurumi ./frp
# Optional UI check (Playwright + Chromium installed separately):
python3 scripts/browser-smoke.py ./kurumi
```

`frp-smoke.py` runs a disposable real TCP roundtrip through frps/frpc on loopback.
`manager_test.go` and `runtime_test.go` test APIs, encryption, persistence, validation,
auth and process ownership. The subprocess fixture is explicitly not a real frp test.
CI checks Linux and Windows, cross-compiles Linux ARM64, and runs the official frp
integration on Linux. See [validation evidence](docs/VALIDATION.md) for actual results
and environment limitations; workflow presence is not proof of a passed CI run.
