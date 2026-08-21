<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/websocket/

Trojan-over-WebSocket transport. Sits between `tls` and `trojan` when enabled.

## Files

- `tunnel.go`, `config.go` — `WebsocketConfig{Enabled, Path, Host}`
- `client.go` — dials WS over the inner TLS conn; uses `gorilla/websocket`
- `server.go` — HTTP server that upgrades matching `Path` to WS, forwards others to fallback
- `conn.go` — adapts `*websocket.Conn` to `net.Conn`; handles framing

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

Client sends `Host: config.Host` (or falls back to SNI). Mismatch at server → fallback, not error. CDN front-compatibility.

## Conventions

- WS message type is `BinaryMessage` always. Text frames break trojan's binary stream.
- `conn.go` buffers partial reads — WS is message-oriented, `net.Conn` is stream-oriented.

## Anti-patterns

- Don't rely on `gorilla/websocket`'s `ReadMessage` line-by-line — it allocates per message. Use the provided `conn.go` wrapper.
- Don't log the full request URL at Info — it leaks the secret path.
