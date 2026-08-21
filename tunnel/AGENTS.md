<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/

Core abstraction layer. `Tunnel` is the sole plugin interface; 14 implementations live here.

## Interface contract (`tunnel.go`)

```go
type Tunnel interface {
    Name() string
    NewClient(ctx context.Context, client Client) (Client, error)
    NewServer(ctx context.Context, server Server) (Server, error)
}
```

- `Client` = `Dialer` + `io.Closer`; `Server` = `Listener` + `io.Closer`.
- Stack composition wraps: outer tunnel receives inner as `client`/`server` arg, delegates transport.
- A tunnel need not implement both roles — `NewClient`/`NewServer` may return `common.NewError("not supported")`. Examples: `tproxy` is server-only, `dokodemo` server-only.

## Metadata (`metadata.go`)

`Metadata{Command, Address}` + `Address{DomainName|IPv4|IPv6, Port, AddressType}`. Wire format: 1-byte cmd + 1-byte addrtype + addr + 2-byte port BE. `ReadFrom`/`WriteTo` on raw `io.Reader`/`Writer`. This is the **trojan wire header**; reused by `simplesocks` and `socks`.

## Registration

`RegisterTunnel(name, t)` called from each subdir's `init()`. `GetTunnel(name)` used by `proxy/stack.go`. No unregister. Re-registration panics.

## Subdirectories (14)

| Tunnel | Role | Notes |
|---|---|---|
| `trojan/` | proto | Main protocol. See `tunnel/trojan/AGENTS.md` |
| `tls/` | transport | TLS + uTLS fingerprint. See `tunnel/tls/AGENTS.md` |
| `websocket/` | transport | See `tunnel/websocket/AGENTS.md` |
| `mux/` | multiplex | smux wrapper. See `tunnel/mux/AGENTS.md` |
| `shadowsocks/` | crypto | See `tunnel/shadowsocks/AGENTS.md` |
| `simplesocks/` | proto | Trojan metadata without password (used inside mux) |
| `freedom/` | outbound | Direct dial. `TODO: hardcoded localhost` in `client.go:79` |
| `transport/` | transport | TCP raw; fallback listener. Import-cycle TODO in `server.go:90` |
| `tproxy/` | inbound | **Linux-only** (`//go:build linux`). IP_TRANSPARENT |
| `socks/` | inbound | SOCKS5 server |
| `http/` | inbound | HTTP/HTTPS proxy server |
| `adapter/` | inbound | Protocol sniffing dispatcher (socks vs http) |
| `router/` | routing | Geosite/geoip. See `tunnel/router/AGENTS.md` |
| `dokodemo/` | inbound | Fixed-target redirect (like v2ray dokodemo-door) |

## Conventions specific to tunnels

- Config retrieved via `config.FromContext(ctx, Name).(*XxxConfig)` — never read JSON directly.
- Tunnel `Name()` MUST equal the config-registry name and the stack-list name. String identity matters.
- Per-tunnel context IDs (from `proxy/proxy.go`) are added BEFORE tunnel construction — use `ctx.Value(Name+"_ID")` if you need session distinction.
- `Conn.Metadata()` returns negotiated `*Metadata` on inbound conns; outbound conns use `Metadata` passed to `DialConn`.

## Anti-patterns

- Don't call `net.Dial` directly in `NewClient` — always use the inner `client` arg. Breaking this breaks stack composition.
- Don't assume `client`/`server` arg is non-nil at the bottom of the stack — leaf tunnels (`freedom`, `transport`, `tproxy`, etc.) ignore it.
