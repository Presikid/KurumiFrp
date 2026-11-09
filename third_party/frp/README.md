# frp source for KurumiFRP

This directory contains the `assets`, `client`, `server`, and `pkg` production
Go sources from the provided frp 0.72.0 source snapshot, together with its
module files and Apache 2.0 license. It preserves the upstream module path so
that KurumiFRP can use it through the replacement in the root `go.mod`.
The manager does not import these packages yet, so `go mod tidy` will remove
the root module's `require` until the in-process integration is added.

The upstream dashboard build is not included. Without an explicitly registered
asset filesystem or a configured assets directory, the upstream dashboard has
no static files. KurumiFRP has its own web interface.

The supplied virtual-network implementation opens TUN devices only on Linux
and macOS; the upstream fallback reports this feature as unsupported on
Windows and other platforms. Keeping the platform-specific files here does not
establish build or runtime support for every architecture.

This import alone does not switch the manager to in-process operation. Before
integrating the client and server services, review process-wide state such as
the frp logger, web assets, metrics, and environment settings: the upstream
services cannot be assumed to isolate those per instance.
