<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/mux/

Multiplexing layer. Reuses one outer conn for many inner streams via `xtaci/smux`.

## Files

- `tunnel.go`, `config.go` — `MuxConfig{Enabled, Concurrency, IdleTimeout}`
- `client.go` — pool of smux sessions; opens streams on demand
- `server.go` — accepts smux sessions, spawns streams to inner tunnel
- `conn.go` — `*smux.Stream` → `tunnel.Conn`; handles trojan-metadata reuse

## Stack position

Client-side: mux is ABOVE trojan, BELOW socks/http inbound:
```
[transport tls trojan mux] ← [adapter socks http]
```

Server reciprocates. Both sides must enable mux — mismatched config = broken stream.

## Session pool (client)

- `Concurrency` = max streams per session; when exceeded, opens new session. `concurrency <= 0` = 不设限（`client.go` 里显式放行）。
- Idle sessions closed after `IdleTimeout` seconds. **`idle-timeout` 为非正数时按配置失误处理，落到 `idleDefaultTimeout`（30s，与配置创建器的默认值同一个常量）**：旧代码只给巡检间隔做兜底，回收判据仍拿原始值比较，`空闲 > 0` 恒真 ⇒ 每个空闲会话在首次巡检就被拆掉，会话复用失效（判据见 `idle_timeout_test.go`，在 pristine HEAD 上是红的）。
- 服务端不对"每条 mux 会话的流数"设限（`handleSession` 里 `AcceptStream` 无上限）。这与"不开 mux 时同一账号本来就能开任意多条 TCP 连接"同阶，不是新增的放大面——别把它当漏洞来"修"。
- `client.go` uses a sync pool keyed by target — one pool per distinct downstream.

## Header handling

**CRITICAL（见 `tunnel/mux/conn.go` 中 `ReadFrom` 循环里的原注释）:**
> `// NEVER STORE THE POINTER TO HEADER, COPY THE HEADER INSTEAD`

smux stream headers are reused across streams by the library. Storing the pointer = later writes clobber earlier stored state. **Always `copy()` before retaining.** Applies to any field on `smux.Stream`'s header struct.

## Conventions

- Stream-level metadata lives in inner trojan/simplesocks framing, NOT smux. mux itself is protocol-agnostic.
- `simplesocks` (not `trojan`) is used inside mux — no password rehash per stream, already authenticated at session level.

## Anti-patterns

- Don't enable mux on UDP-heavy workloads — smux is TCP-stream; UDP still goes direct.
- Don't tune `Concurrency` above ~8 without testing; head-of-line blocking degrades worse than multiple sessions.
