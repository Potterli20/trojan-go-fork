<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/

Core abstraction layer. `Tunnel` is the sole plugin interface; 15 implementations live here.

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

## Subdirectories (15)

| Tunnel | Role | Notes |
|---|---|---|
| `trojan/` | proto | Main protocol. See `tunnel/trojan/AGENTS.md` |
| `tls/` | transport | TLS + uTLS fingerprint. See `tunnel/tls/AGENTS.md` |
| `websocket/` | transport | See `tunnel/websocket/AGENTS.md` |
| `mux/` | multiplex | smux wrapper. See `tunnel/mux/AGENTS.md` |
| `shadowsocks/` | crypto | See `tunnel/shadowsocks/AGENTS.md` |
| `simplesocks/` | proto | Trojan metadata without password (used inside mux) |
| `freedom/` | outbound | 直连出站；可选 forward-proxy（SOCKS5）与 `outbound_local_addr`/fwmark。历史上文档记的 “client.go:79 hardcoded localhost TODO” 已不存在，包内无任何 TODO |
| `transport/` | transport | TCP raw; fallback listener。关于 “导入 websocket/http 会成 import cycle” 的注释在 `server.go` 里（旧文档记的行号已漂移，按内容找） |
| `tproxy/` | inbound | **Linux-only** (`//go:build linux`). IP_TRANSPARENT |
| `socks/` | inbound | SOCKS5 server |
| `http/` | inbound | HTTP/HTTPS proxy server。h1 之外还嗅探 h2c 前文并整条交给 `h2.go`；h2 上的 CONNECT 显式回 501；没有任何目标主机时回 400（见下面 Conventions） |
| `adapter/` | inbound | Protocol sniffing dispatcher (socks vs http) |
| `router/` | routing | Geosite/geoip. See `tunnel/router/AGENTS.md` |
| `dokodemo/` | inbound | Fixed-target redirect (like v2ray dokodemo-door) |
| `quic/` | transport | QUIC 承载；服务端/客户端都显式开 RFC 9221 datagram。**标准 run-type 的栈里没有它，只有 `custom` 模式能到达**；trojan 的 UDP 走 stream 而非数据报 |

## Conventions specific to tunnels

- Config retrieved via `config.FromContext(ctx, Name).(*XxxConfig)` — never read JSON directly.
- Tunnel `Name()` MUST equal the config-registry name and the stack-list name. String identity matters.
- **没有** per-tunnel 实例 ID：`ctx.Value(Name+"_ID")` 永远取不到值（原先唯一的写入点已删除，且它的 key 类型是包内私有的、别人根本读不到）。需要按会话区分实例请另想机制。
- `Conn.Metadata()` returns negotiated `*Metadata` on inbound conns; outbound conns use `Metadata` passed to `DialConn`.
- **线上格式里的长度/偏移一律先升到 `int` 再参与加法。** `metadata.go` 里
  `make([]byte, length+2)`（`length` 是 `byte`）在 254/255 处回绕成 0/1，缓冲比域名还短，
  下一句 `buf[:length]` 直接切片越界 panic。影响面是全仓共用的握手解码：实测（HEAD 二进制）
  未认证的本地 SOCKS5 入口收到一个 255 字节主机名就整个进程崩掉——`socks` 这条链上没有
  recover（只有 `mux`/`tls`/`trojan` 三处有）。门禁见 `tunnel/metadata_wire_test.go`。
- **`http` 入站不许把"没有目标"退化成默认端口。** `resolveProxyTarget`（`server.go`）是
  h1 与 h2c 两条分支共用的唯一入口：`Host: example.com` 不带端口 → 按 80 转发（合法）；
  **完全没有** Host/`:authority` → 明确回 400。旧实现落到 `NewAddressFromHostPort("tcp","",80)`，
  目的地字符串是 `":80"`，而实测 `net.Dial("tcp", ":80")` **连的是 127.0.0.1** ——
  等于让代理入口替请求方访问代理自己的本地服务，且域名是空串时按域匹配的路由规则
  一条都不会命中（HEAD 上实测：一个不带 Host 的请求被正常转发、回 204）。
  判据在 `tunnel/http/target_required_test.go`，含"不带端口的合法 Host 仍须走 80"的对照。
- **relay 没有半关闭**：`proxy/proxy.go` 的 `relayConnLoop` 是「任一方向结束就拆掉整个会话」
  （两个 `io.CopyBuffer` 共享一个 `done`，谁先返回谁触发，随后 `defer` 关掉两端）。
  `tunnel.Conn` 接口也没有 `CloseWrite`，所以无法只断一个方向。写 tunnel 时必须假设：
  只要有一方读到 EOF/错误，另一方的数据就没了。典型受害者是「请求写完就 EOF」的形态——
  `tunnel/http` 因此把请求方向的 EOF 推迟到本请求会话真正结束（见 `server.go` 里
  `OtherConn.Close` 的注释）；任何新 tunnel 若自己会先产生 EOF，也要照这个思路处理。

## Anti-patterns

- Don't call `net.Dial` directly in `NewClient` — always use the inner `client` arg. Breaking this breaks stack composition.
- Don't assume `client`/`server` arg is non-nil at the bottom of the stack — leaf tunnels (`freedom`, `transport`, `tproxy`, etc.) ignore it.
