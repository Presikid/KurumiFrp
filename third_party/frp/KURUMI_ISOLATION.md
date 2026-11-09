# Kurumi embedded client: restricted profile

client.KurumiRestrictedClientProfileVersion == 1 identifies the restricted
ServiceOptions{Isolated: true} API used by this workspace's
internal/embedded/frp_client.go. It is not the broader
KurumiEmbeddedIsolationVersion requested by the original 02 adapter. That
broader marker is intentionally undefined. Importing the module or setting
kurumi_frp_patched alone does not select the restricted profile.

The adapter makes private configuration copies, calls
CompleteWithoutProxyEnvironment, and sets Isolated. The 04 parser copy also
uses this completion method; using upstream Complete beforehand can already
have inherited the host proxy value. Explicit upstream proxy URLs are rejected.
Without a sink, the profile's contextual logger discards its messages. An
explicitly supplied, single-use instance sink captures contextual logs; its
records can contain sensitive upstream details. Neither choice establishes that
every shared library avoids the process-global logger. Adapter and configuration
errors omit untrusted detail.

Client package initialization no longer writes quic-go environment variables
or golib's global salt. Legacy control, proxy and visitor streams and NAT-hole
messages use a local fixed frp salt and the existing AES-CFB format. The client
never assigns net.DefaultResolver: custom dnsServer is rejected for all client
services until scoped resolution is implemented. Removing the QUIC environment
overrides can change QUIC warnings and ECN behavior for non-isolated callers.

Run is single-use. Close before Run releases constructed resources; concurrent
Close cancels and joins login/reconnect, prevents subsequent control
publication, closes the connector's tracked control and work connections, and
waits for the control worker. TCP/websocket handshakes are interrupted on
cancellation. Custom connectors and callbacks are not accepted in this profile.
These lifecycle changes still require validation in the combined workspace and
are not a runtime guarantee.

The profile rejects custom DNS, virtual networking, admin and metrics servers,
includes, store sources, OIDC and dynamic token sources, KCP/QUIC transport,
XTCP, upstream proxies, client/visitor plugins, health checks, custom hooks and
unsafe feature overrides. The adapter returns ErrFRPUnsupported for these
options. Other upstream users retain normal configuration completion and use
the process logger unless they explicitly supply an instance sink or logger.

Each restricted control generation now owns a separate cancellation scope.
Its dispatcher, admitted proxy and visitor workers, accepted connections and
UDP forwarding sockets are tracked; shutdown cancels and joins the admitted
work. UDP and SUDP sessions signal cancellation without closing channels still
used by senders. This is a source-level design, not proof that every dependency
or error path has drained. The 05 RuntimeInstance.Wait contract requires
resources to be safe to replace; do not claim that contract is met without
independent verification. No real client/server interoperability or concurrent
runtime verification has been performed for the combined changes.

ValidateFRPClient runs the same restricted configuration preparation as the
constructor without creating a service. It neither claims nor validates an
instance's traffic bindings or log sink, and successful preflight cannot promise
that later construction or startup will succeed.

The integrated server fork has per-service metrics, per-service 404 pages,
tracked workers and accepted connections, and bounded shutdown with a way to
await incomplete cleanup. Server tracking is per service, not proof that every
old control generation or external dependency has drained. Importing the
server adapter still runs server package initialization that mutates the process environment and golib's
salt. Logging and dashboard assets also retain process-wide state. The server
wrapper can return upstream errors and warnings; callers must not expose those
blindly. The parser returns stage-only diagnostics and warnings because raw TOML
can itself contain credentials. Shared crypto and logging changes still require
real interoperability and concurrent runtime validation. Telemetry boundaries,
instance log ownership, and the remaining manager wiring are documented in
`docs/ENGINE_TRAFFIC_LOGS_INTEGRATION.md` at the repository root.
The manager continues to use its existing external-process path; the draft
bridge from the source snapshot is not included or enabled here.
