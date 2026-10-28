<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/websocket/

Trojan-over-WebSocket transport. Sits between `tls` and `trojan` when enabled.

## Files

- `tunnel.go`, `config.go` — `WebsocketConfig{Enabled, Path, Host}`
- `client.go` — dials WS over the inner TLS conn; uses `coder/websocket` (v1.8)
- `server.go` — HTTP server that upgrades matching `Path` to WS, forwards others to fallback
- `conn.go` — adapts `*websocket.Conn` to `net.Conn` (`NetConn` stream wrapper),
  counts bytes for the tracker and wakes blocked relays on `Close`

## Stack position

```
[transport tls websocket trojan]  # client stack when WS enabled
```

Websocket `NewClient/NewServer` receives tls as inner and wraps it. Does NOT dial TCP itself — TLS has already done that.

## Path routing (server)

Server's HTTP mux:
- `config.Path` (e.g. `/ws`) → upgrade to WS, feed to outer trojan tunnel
- everything else → fallback (`redirector/` or configured target) — makes the endpoint look like a normal HTTPS site

## Host header

Client sends `Host: config.Host` (or falls back to `remote-addr`). The server does **not**
check it: `AcceptOptions.InsecureSkipVerify = true`, because Host/Origin mismatch is the
normal case behind a CDN and the real authentication is the trojan password. Server-side
`websocket.host` is only a fallback for the *decoy* address when `remote-addr` is empty
(`NewServer`) — writing it on the server changes nothing else.

## Handshake window (server)

`AcceptConn` runs serially inside the inbound accept loop, so anything that blocks there
stops the whole inbound. The handshake is therefore bounded in **both** directions:
`conn.SetDeadline(handshakeTimeout)` covers reading the upgrade request *and* writing the
101 response (a peer that stops reading — TCP zero window — would otherwise pin it forever),
and the deadline is cleared before the conn is handed upstream. In-flight handshakes are
registered in `pendingConns`; `Close` closes them (`closePending`) instead of waiting out
the 30s. Repro: `handshake_stall_test.go` (net.Pipe peer that reads the request and never
reads again).

## Conventions

- **握手期间的资源收尾有两条 defer，成对且互不相同**：`connHandedOff` 决定这条连接由谁负责
  （移交上游 / 交给 redirector 拷贝时置位，否则 panic 或提前返回时由本层关掉），`handedOff`
  只决定握手子 ctx 是否被取消。只写后者会漏 fd：panic 落在 `trackPending` 之前时连接既不在
  在途登记表（`Close` 的排空够不着）也没有活引用，只能等运行时的 netFD finalizer。
  判据 `handshake_panic_close_test.go`，注入点 `var readUpgradeRequest`。
- WS message type is `BinaryMessage` always. Text frames break trojan's binary stream.
- `conn.go` buffers partial reads — WS is message-oriented, `net.Conn` is stream-oriented.

## Anti-patterns

- Don't rely on message-oriented `Read` semantics — `NetConn` flattens frames into a stream,
  and `SetReadLimit(maxMessageSize)` (1 MiB) applies per *frame*: a stalled writer gets an
  error, never a silent drop.
- Don't set a read-only deadline for the handshake. The 101 response is a *write*, and coder's
  `Accept` flushes it synchronously.
- Don't log the full request URL at Info — it leaks the secret path.
