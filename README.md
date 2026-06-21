# proxyproxy

A SOCKS5 proxy load balancer. It accepts SOCKS5 client connections and forwards
them through a pool of upstream SOCKS5 proxies (round-robin), while collecting
per-upstream statistics exposed via a small web UI and JSON API.

Targets macOS and Linux.

## Features

- SOCKS5 server (CONNECT command, no-auth, IPv4/IPv6/domain targets)
- Round-robin load balancing across upstream SOCKS5 proxies
- Upstream proxy authentication (`socks5://user:pass@host:port`)
- Stats per upstream: requests, connections, active conns, bytes sent/received, errors, last used
- Optional client-IP logging
- Web UI (auto-refreshing) at `web_listen`
- JSON API at `web_listen`/api/stats

## Build

```
go build -o bin/proxyproxy ./cmd/proxyproxy
```

## Configure

Copy `config.example.yaml` to `config.yaml` and edit:

```yaml
listen: ":1080"
web_listen: ":8080"
upstream_proxies:
  - "socks5://127.0.0.1:1081"
  - "socks5://user:pass@127.0.0.1:1082"
log_ips: false
```

## Run

```
./bin/proxyproxy -config config.yaml
```

Point your SOCKS5 client at `listen` (default `:1080`), then open
`http://localhost:8080/` to view stats.

## API

`GET /api/stats` returns:

```json
{
  "total_requests": 0,
  "total_bytes_sent": 0,
  "total_bytes_recv": 0,
  "total_connections": 0,
  "total_errors": 0,
  "start_time": 0,
  "proxies": [
    {
      "address": "127.0.0.1:1081",
      "requests": 0, "connections": 0, "active_conns": 0,
      "bytes_sent": 0, "bytes_recv": 0, "errors": 0, "last_used": 0
    }
  ],
  "clients": [
    { "ip": "1.2.3.4", "requests": 0, "last_seen": 0, "bytes_sent": 0, "bytes_recv": 0 }
  ]
}
```

The `clients` array is only present when `log_ips: true`.

## Notes

- Only the SOCKS5 CONNECT command is supported (covers HTTP/HTTPS/raw TCP via SOCKS5).
- `log_ips` is off by default; enable it only if you are comfortable collecting
  client source IPs.
