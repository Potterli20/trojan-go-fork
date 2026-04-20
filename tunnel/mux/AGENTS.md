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

- `Concurrency` = max streams per session; when exceeded, opens new session.
- Idle sessions closed after `IdleTimeout` seconds.
- `client.go` uses a sync pool keyed by target — one pool per distinct downstream.

## Header handling

**CRITICAL (`conn.go:54`):**
> `// NEVER STORE THE POINTER TO HEADER, COPY THE HEADER INSTEAD`

smux stream headers are reused across streams by the library. Storing the pointer = later writes clobber earlier stored state. **Always `copy()` before retaining.** Applies to any field on `smux.Stream`'s header struct.

## Conventions

- Stream-level metadata lives in inner trojan/simplesocks framing, NOT smux. mux itself is protocol-agnostic.
- `simplesocks` (not `trojan`) is used inside mux — no password rehash per stream, already authenticated at session level.

## Anti-patterns

- Don't enable mux on UDP-heavy workloads — smux is TCP-stream; UDP still goes direct.
- Don't tune `Concurrency` above ~8 without testing; head-of-line blocking degrades worse than multiple sessions.
