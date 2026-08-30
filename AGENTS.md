<!-- Generated: 2026-04-20 | Commit: 02d5c12 | Branch: latest -->

# trojan-go

Go implementation of the Trojan proxy protocol. Unmaintained upstream (archived), but still built/released via CI. Module: `github.com/p4gefau1t/trojan-go`, Go 1.17, CGO disabled by default.

## Architecture

Everything is a **Tunnel**. A running instance is a stack of tunnels composed inside-out (inbound → ... → outbound). `proxy/stack.go::CreateClientStack`/`CreateServerStack` iterate a name list, call `tunnel.GetTunnel(name).NewClient(ctx, prev)` / `NewServer(ctx, prev)`, and wrap each layer around the previous one. The final `proxy.Proxy` (see `proxy/proxy.go`) runs `source.AcceptConn → sink.DialConn` + `source.AcceptPacket → sink.DialPacket` relay loops with context-cancel unblock.

All modules self-register in `init()`. Nothing is wired explicitly in `main.go` — side effects via blank import of `component/` register tunnels, proxies, config creators, option handlers, loggers. See `component/AGENTS.md` for build-tag selection.

## Registries (where to look first)

- `tunnel.RegisterTunnel(name, Tunnel)` → `tunnel/tunnel.go`
- `proxy.RegisterProxyCreator(name, fn)` → `proxy/proxy.go` (client, server, forward, nat, custom)
- `config.RegisterConfigCreator(name, fn)` → `config/config.go`
- `option.RegisterHandler(Handler)` → `option/option.go` (startup mode selector, not CLI flags)
- `log.RegisterLogger(Logger)` → `log/log.go`

## Entry & flow

1. `main.go`: `flag.Parse()` → loops `option.PopOptionHandler().Handle()`; first handler whose `Handle()` returns non-`common.NewError("not set")` wins.
2. Default handler builds a `proxy.Proxy` via `proxy.NewProxyFromConfigData(data, json|yaml)`.
3. Config JSON/YAML → `config.WithJSONConfig`/`WithYAMLConfig` → stores per-module structs in `ctx` under `MODULE_CONFIG` key; modules retrieve via `config.FromContext(ctx, Name).(*FooConfig)`.
4. `CreateClientStack(ctx, names...)` builds tunnel stack; `proxy.NewProxy(ctx, cancel, sources, sink)` runs it.

## Code map

- `main.go` — entry, flag parsing, option loop
- `tunnel/` — core abstractions + 14 tunnel impls. See `tunnel/AGENTS.md`
- `proxy/` — Proxy lifecycle, stack builder, proxy creators. See `proxy/AGENTS.md`
- `component/` — build-tag-gated blank imports; THE plugin loader. See `component/AGENTS.md`
- `config/` — context-based config registry. See `config/AGENTS.md`
- `option/` — startup handler registry. See `option/AGENTS.md`
- `common/` — errors, `Must`/`Must2`, rate limiter
- `log/` — leveled logger interface + `golog`, `simplelog` impls (self-register)
- `api/service/` — gRPC stats/user mgmt; generated pb. See `api/service/AGENTS.md`
- `statistic/` — user traffic/auth backends: memory, mysql, sqlite. See `statistic/AGENTS.md`
- `redirector/`, `recorder/` — misdirection on auth failure
- `test/`, `tunnel/*/*_test.go` — integration tests require loopback + test certs in `test/`
- `url/` — `trojan://` / `trojan-go://` URL parser

## Conventions

- **Errors**: use `common.NewError(msg)` and `.Base(err)` — concatenates `" | " + err.Error()`. No `Unwrap` chain. `errors.Is`/`errors.As` DO NOT WORK across `.Base()` boundaries. See `common/error.go:15-20`.
- **Panics via `Must`**: `common.Must(err)`, `common.Must2(x, err)` panic on non-nil err. Used at startup for unrecoverable wiring (`common/error.go:32-44`).
- **Log levels are INVERTED**: `0=AllLevel, 1=InfoLevel, 2=WarnLevel, 3=ErrorLevel, 4=FatalLevel, 5=OffLevel` (`log/log.go:12-19`). Lower = more verbose.
- **Per-instance IDs**: `context.WithValue(ctx, Name+"_ID", rand.Int())` — each proxy creator stamps its own ID into context (`proxy/proxy.go:184-187`). Enables multiple instances of the same tunnel in one process.
- **Context cancel is the shutdown primitive**: relay loops in `proxy/proxy.go:51-61, 83-91` select on `ctx.Done()` to exit `Accept` blocking calls. All `Tunnel.NewClient/NewServer` implementations must respect `ctx`.

## Anti-patterns (project-specific)

- **Never store the pointer to a mux header — copy it.** `tunnel/mux/conn.go:54` (verbatim comment).
- **`errors.Is`/`As` won't traverse `.Base()`.** Check messages or keep original error separately if you need it.
- **Don't edit `api/service/api.pb.go` or `api_grpc.pb.go`** — regenerate via `api/service/gen.sh`.
- **`SHADOWSOCKS_SF_CAPACITY="-1"` is required for tests** (disables shadowsocks stream capacity check in go-shadowsocks2). See `Makefile::test` and CI.

## Build tags

Set via `go build -tags "..."`. Selection happens entirely through `component/*.go` `//go:build` lines.

- `full` — default Makefile target; pulls everything
- `mini` — trojan core only (no forward/nat/api/mysql)
- `client`, `server`, `forward`, `nat`, `custom` — mode-specific subsets
- `api` — gRPC API server
- `mysql`, `sqlite` (linux+cgo only) — auth backends
- Platform: `tproxy` and `nat` proxy are Linux-only (`tunnel/tproxy/`, `proxy/nat/nat.go`)

## Commands

```
make             # CGO_ENABLED=0 go build -tags "full" -trimpath -o build/trojan-go
make test        # SHADOWSOCKS_SF_CAPACITY="-1" go test -v ./...
make test-race   # SHADOWSOCKS_SF_CAPACITY="-1" go test -count=1 -race -tags full ./...
make release     # cross-compile matrix + zip (see Makefile)
./build/trojan-go -config config.json
```

CI (`.github/workflows/`): `go.yml` is a 6-hourly dependency-maintenance job (`gofmt -r 'interface{} -> any'`, `go fix`, `go mod tidy -compat=1.27`, `gomod/gomod.sh`, then force-push); `main.yml` runs the weekly cross-compile release matrix; `codeql.yml` security analysis; `docker-build.yml` image build; `dependency-review.yml` dependency review on PRs. Note: no workflow runs the test suite — run `make test` / `make test-race` locally.

## Notes

- Upstream is archived. Fork-friendly; no contribution process.
- No `go:generate` directives anywhere. Protobuf regen is manual (`api/service/gen.sh`).
- `tunnel/tls/fingerprint/` vendors uTLS ClientHello IDs — updating uTLS may require touching this list.
- `tunnel/router/` uses v2ray's geosite/geoip data format; bundles no data — user supplies `.dat`.

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.

## 5. 语言要求

- 所有回答和交流必须使用中文
- 代码注释使用中文（除非项目规范要求英文）
- Git commit message 使用中文
- TODO 列表和任务描述使用中文