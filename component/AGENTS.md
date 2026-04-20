<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# component/

The plugin loader. Every file is blank-import side effects gated by build tags.

## How it works

`main.go` imports `github.com/p4gefau1t/trojan-go/component` (blank). Each file in this dir does:

```go
//go:build TAG
// +build TAG

package component

import (
    _ "github.com/p4gefau1t/trojan-go/tunnel/xxx"
    _ "github.com/p4gefau1t/trojan-go/proxy/yyy"
)
```

The transitive `init()` functions register the actual tunnels/proxies/configs/options. **This file is the ONLY place** where `-tags` values directly gate code.

## Files and tags

| File | Tags | Includes |
|---|---|---|
| `base.go` | `full` `mini` `client` `server` `custom` `forward` `nat` | `tunnel/{trojan,tls,freedom,transport,socks,http,mux,simplesocks,shadowsocks,adapter,dokodemo,websocket,router}`, log impls |
| `client.go` | `full` `client` `custom` | `proxy/client` |
| `server.go` | `full` `server` `custom` | `proxy/server` |
| `forward.go` | `full` `forward` `custom` | `proxy/forward` |
| `nat.go` | `full` `nat` `custom` `linux` | `proxy/nat`, `tunnel/tproxy` |
| `custom.go` | `full` `custom` | `proxy/custom` |
| `api.go` | `full` `api` `custom` | `api/service` |
| `mysql.go` | `full` `mysql` | `statistic/mysql` |
| `other.go` | (various) | option handlers — `option/{default,version,test,info}` |

## Conventions

- **Every new tunnel/proxy MUST get a blank import here**, gated by the right tag, or it will never be registered at runtime.
- **Default tag = `full`** (set by Makefile). Distributors wanting a smaller binary pick `mini` or role-specific tags.
- `mini` excludes: `forward`, `nat`, `api`, `mysql`, `sqlite`. Minimum viable trojan client/server only.

## Anti-patterns

- Don't add `import _ "…"` inside `tunnel/xxx/tunnel.go` cross-referencing another tunnel — causes cycles and bypasses the tag gate. Cross-tunnel references go through `tunnel.GetTunnel(name)`.
- Don't use custom build tags that aren't already in this file — they won't compose with the release matrix.
