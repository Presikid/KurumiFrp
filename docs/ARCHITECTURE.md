# Architecture and security boundary

The Go executable embeds `web/` using go:embed. No npm, Docker or remote service
is needed. All manager API operations use a single serialized application mutex;
process exits/log capture use independent locks. Per-object revision writes reject
stale forms with HTTP 409. Configuration mutation is not a process mutation.

`state.json` holds the current structured configuration, encrypted secret values,
30 previous states and 200 configuration events. Writes use temporary files, sync
and rename; readers only use the published in-memory state after a successful write.
Crash-durability details of rename/fsync differ by OS; this is not a transactional
multi-host database. A single-owner lock prevents accidental concurrent managers.

AES-GCM ciphertext is authenticated using a private 32-byte local master key.
Runtime TOML contains Go-template environment references, not raw credentials;
the child receives TOML-escaped values in its environment. The same OS account and
administrators can inspect process memory/environment and read the key. This design
protects against casual disclosure of state files, not against a compromised host.
Avoid unrestricted backups, process dumps and diagnostic environment exports.

The public configuration view strips ciphertext and plaintext. Blank secret fields
on edits preserve existing values. All instance auth tokens and STCP secrets must
have at least eight bytes. Session cookies stay in memory and expire after twelve
hours; a manager restart invalidates sessions. Logging out revokes that session, not
the local access token. To rotate the access token, stop the manager, remove only
`access.token`, and restart. Do not remove `master.key`.

Input is typed and rendered; no raw TOML uploads, plugin/exec configuration, browser
binary paths, shell commands or unauthenticated control endpoints are supported.
frp verification and launch execute only the operator-installed binary with fixed
arguments and no shell. The binary itself is trusted and must not be web-writable.
User-selected target addresses are intentional tunnel configuration, not constrained
by a network sandbox. frps proxy endpoints may publish services; choose them carefully.

Generated frp transport enables TLS; it does not provision a custom CA, certificate
pinning or mutual TLS. Do not infer peer identity verification from encryption alone.
A future connection policy layer must add those controls without weakening defaults.

Apply sequence: render -> bounded official verify -> optional stop -> spawn ->
observe real process exit. Verify failure leaves the old process alone. A successful
spawn is not a registration check. There is no automatic rollback on post-spawn failure.
Future hot reload must distinguish proxy changes from global process settings.

Logs are bounded, known secrets are scrubbed across fragmented writes, and oversized
lines are dropped. Logs are memory-only and are not a complete durable audit trail.
Only saved draft changes currently populate the persistent configuration event list.

References reviewed for the implementation:
- https://gofrp.org/en/docs/features/common/configure/
- https://gofrp.org/en/docs/features/common/client/
- https://gofrp.org/en/docs/reference/server-configures/
