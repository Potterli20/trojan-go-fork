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

`NewProxyFromConfigData`（`proxy/proxy.go`）里：
```go
ctx := context.WithValue(context.Background(), proxyIDKey(Name+"_ID"), instanceID)
```
`instanceID` 来自 `common.SecureRandInt`（不是 `math/rand`）；`proxyIDKey` 是私有
string 类型，避免与包外 key 碰撞。**注意现实：只有这一处写入，key 固定是
`"PROXY_ID"`，且没有任何 tunnel 读取 `*_ID`** —— 旧文档说的“每个 creator 各打一个、
下游按 per-session stats 读取、同类型多实例靠它区分”都已不成立。

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
