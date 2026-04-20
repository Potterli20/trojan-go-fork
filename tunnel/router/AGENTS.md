<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/router/

Rule-based outbound selector. Routes connections to `proxy` / `direct` / `block` based on destination.

## Files

- `tunnel.go`, `config.go` — `RouterConfig{Enabled, Bypass, Proxy, Block, DomainStrategy, DefaultPolicy, Geosite, Geoip}`
- `client.go` (454 lines) — rule engine, geo data loader, strategy resolver
- `server.go` — placeholder (router is client-only outbound layer)

## Role in stack

Client-side only. Sits OUTSIDE trojan, picks which inner client to dial through:

```
         ┌─ proxy → [transport tls trojan …] → remote trojan server
router ──┼─ direct → [freedom] → target directly
         └─ block  → reject
```

Router's `DialConn(addr, _)` picks a policy per `addr`, then delegates to the matching inner client stored at construction.

## Rule matching

Order: explicit `domain` > `geosite` > `ip` > `geoip` > default. First match wins.

- `DomainStrategy`: `AsIs` | `IPIfNonMatch` | `IPOnDemand` — controls whether to resolve domain → IP for `geoip` rules.
- `Geosite` / `Geoip`: filenames of v2ray `.dat` files. **Not bundled.** User supplies path. Loader uses v2ray's protobuf schema (`v2fly/v2ray-core/app/router/config.pb.go`).

## Default policies

- `Bypass` = direct-list (lan, cn)
- `Proxy` = force-proxy-list
- `Block` = drop-list
- `DefaultPolicy` = `"proxy"` | `"direct"` | `"block"` when no rule matches

## Conventions

- Geo data loaded once at startup, held in memory (~5-15MB).
- Tag strings are case-sensitive and match v2ray's conventions (e.g. `geosite:cn`, `geoip:private`).

## Anti-patterns

- Don't call `net.LookupHost` synchronously in the hot path — use `DomainStrategy: AsIs` for pure-domain routing.
- Don't ship geo data in the binary; keep it a runtime file for updatability.
