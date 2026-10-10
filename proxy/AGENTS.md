<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# proxy/

Stack builder + relay engine. Turns config + tunnel names into a running `Proxy`.

## Core types

- `Proxy` (`proxy.go`): holds `sources []tunnel.Server`, `sink tunnel.Client`, `ctx`, `cancel`. `Run()` spawns `relayConnLoop` + `relayPacketLoop` per source.
- `Creator func(ctx) (*Proxy, error)` registered via `RegisterProxyCreator(name, fn)` — names: `client`, `server`, `forward`, `nat`, `custom`.
- `NewProxyFromConfigData(data []byte, isJSON bool) (*Proxy, error)`（`proxy/proxy.go`）——按 `general.RunType` 分派到对应 creator。

## Stack builder (`stack.go`)

```go
CreateClientStack(ctx, path ...string) (tunnel.Client, error)
CreateServerStack(ctx, path ...string) (tunnel.Server, error)
```

Iterates names left-to-right. For each name: `tunnel.GetTunnel(name).NewClient(ctx, prev)`. First name = innermost (leaf); last = outermost (entry point). Stack lists are hardcoded per proxy creator (e.g. `client.go` uses `[transport tls trojan]` for sink, `[adapter socks http]` for source).

## Per-instance ID

该机制**已删除**（曾经在 `NewProxyFromConfigData` 里写入 `proxyIDKey(Name+"_ID")`）：
`proxyIDKey` 是包内私有类型，包外无法构造同一个 key 去读，包内也没有读者，所以它从来没有作用
（`common.SecureRandInt` 仍被 `tunnel/mux` 的 padding 使用，未受影响）。
若将来要支持同类型 tunnel 多实例并存，需要重新设计一个包外可读的 key。

## Relay loops

- `relayConnLoop`（`proxy/proxy.go`）: `source.AcceptConn(nil)` → `sink.DialConn(meta, nil)` → bidi `io.Copy` in goroutines. `ctx.Done()` closes source to unblock `Accept`.
- `relayPacketLoop`（`proxy/proxy.go`）: same for UDP; `PacketConn.ReadFrom/WriteTo` with `Metadata` addressing.

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

- 别漏 goroutine：转发两侧的 conn 必须在任一侧结束时关闭（`proxy/proxy.go` 用 `defer` + `sync.OnceFunc(closeDone)`，且池化 buffer 借出后一律 `Put` 归还）。
- Don't `log.Fatal` in creators — return error; `main.go` handles exit.
