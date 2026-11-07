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
