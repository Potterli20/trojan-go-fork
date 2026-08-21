<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# proxy/

Stack builder + relay engine. Turns config + tunnel names into a running `Proxy`.

## Core types

- `Proxy` (`proxy.go`): holds `sources []tunnel.Server`, `sink tunnel.Client`, `ctx`, `cancel`. `Run()` spawns `relayConnLoop` + `relayPacketLoop` per source.
- `Creator func(ctx) (*Proxy, error)` registered via `RegisterProxyCreator(name, fn)` — names: `client`, `server`, `forward`, `nat`, `custom`.
- `NewProxyFromConfigData(data []byte, isJSON bool) (*Proxy, error)` (`proxy.go:143`) — dispatches on `general.RunType`.

## Stack builder (`stack.go`)

```go
CreateClientStack(ctx, path ...string) (tunnel.Client, error)
CreateServerStack(ctx, path ...string) (tunnel.Server, error)
```

Iterates names left-to-right. For each name: `tunnel.GetTunnel(name).NewClient(ctx, prev)`. First name = innermost (leaf); last = outermost (entry point). Stack lists are hardcoded per proxy creator (e.g. `client.go` uses `[transport tls trojan]` for sink, `[adapter socks http]` for source).

## Per-instance ID

`proxy.go:184-187`:
```go
ctx = context.WithValue(ctx, name+"_ID", rand.Int())
```
Stamped by every proxy creator at start. Downstream tunnels read it for per-session stats/logging. **Multiple same-type tunnels in one stack each need distinct IDs** — this is why per-instance and not package-global.

## Relay loops

- `relayConnLoop` (`proxy.go:51-61`): `source.AcceptConn(nil)` → `sink.DialConn(meta, nil)` → bidi `io.Copy` in goroutines. `ctx.Done()` closes source to unblock `Accept`.
- `relayPacketLoop` (`proxy.go:83-91`): same for UDP; `PacketConn.ReadFrom/WriteTo` with `Metadata` addressing.

## Subdirectories

- `client/` — `[transport tls trojan mux?] ← [adapter socks http]`; mux optional via config
- `server/` — inbound trojan over tls; dispatches to `freedom` / `simplesocks` / redirect
- `forward/` — fixed client target (no SOCKS/HTTP inbound)
- `nat/` — **Linux-only**; uses `tproxy` source
- `custom/` — fully user-specified stack lists from config

## Conventions

- **Never block `Accept` without context awareness** — wrap with `ctx.Done()` select, or close source on cancel.
- **Metadata flows through `DialConn(addr, conn)`** — the outer tunnel reads inner's `Metadata()`, re-emits on its own dial.
- **UDP is separate** from TCP in the stack abstraction — `PacketConn` lives on same `Client`/`Server` but has independent accept loop.

## Anti-patterns

- Don't add goroutine leaks: every `io.Copy` pair must close both conns on either side's EOF (`proxy.go` uses `defer` + `sync.Once`).
- Don't `log.Fatal` in creators — return error; `main.go` handles exit.
