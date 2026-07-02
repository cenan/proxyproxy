# AGENTS.md

## Commands

```
go build -o bin/proxyproxy ./cmd/proxyproxy   # build binary
go build ./...                                # compile-check all packages
go vet ./...                                  # vet (run before finishing any change)
go mod tidy                                   # sync deps after editing imports
```

There is no test suite. Verify changes by running the binary against real upstream
SOCKS5 proxies (see `config.example.yaml`) and hitting `GET http://<web_listen>/api/stats`.

The toolchain pins `go 1.25.0` in `go.mod`; the local `go` command may auto-download
a newer toolchain to satisfy `golang.org/x/net`'s minimum.

## Architecture

Single-binary SOCKS5 load balancer. Entry point: `cmd/proxyproxy/main.go`.

- `internal/proxy` — hand-rolled SOCKS5 server (no third-party SOCKS lib). Parses the
  handshake/request itself in `socks5.go`, then dials the target through an upstream
  SOCKS5 proxy via `golang.org/x/net/proxy`. Round-robin across upstreams.
- `internal/stats` — thread-safe counters (atomics on per-upstream structs kept in
  insertion order). `Snapshot()` is what the web layer reads.
- `internal/web` — HTTP UI. The HTML is embedded from `internal/web/index.html` via
  `//go:embed`; editing that file rebuilds the UI with no other wiring.
- `internal/config` — YAML loader with defaults; rejects an empty `upstream_proxies`.

The root-level `web/` directory is empty and unused — do not put assets there. The
real UI source is `internal/web/index.html`.

## Conventions

- macOS + Linux only; do not add Windows support or guard for it.
- No comments in code unless explicitly requested.
- `log_ips` defaults to false; client IP collection is opt-in.
- Upstream URLs use `socks5://`/`socks5h://` (default for bare `host:port`) or
  `http://`/`https://` (HTTP CONNECT proxies) schemes. Auth via
  `socks5://user:pass@host:port` or `http://user:pass@host:port`.
