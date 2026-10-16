<!-- Generated: 2026-04-20 | Commit: 02d5c12 | Branch: latest -->

# trojan-go

Go implementation of the Trojan proxy protocol. Unmaintained upstream (archived), but still built/released via CI. Module: `github.com/Potterli20/trojan-go-fork`, `go 1.27.1`（go.mod 与 CI 的 `^1.27` 一致），CGO disabled by default.

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

1. `main.go`: `flag.Parse()` → `selectAndRun(option.PopOptionHandler)`; 按 `Priority()` 从高到低逐个试 handler，`nil` = 接管本次运行，`option.NotSet()` = 不适用继续下一个，**其它错误立刻终止**（详见 `option/AGENTS.md`）。
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
- `diag/` — 可选的 pprof 诊断口。**默认不监听任何端口**，只有 `-diag-bind`（`main.go`）给出
  回环地址才起服务；非回环（含 `:6060`、`0.0.0.0:6060`）一律拒绝。Go 1.27 起
  `runtime/pprof` 注册了 `goroutineleak` 剖析类型（跑一轮泄露检测 GC，只输出"阻塞在
  channel/mutex 上且无任何活对象可达"的协程），这是长期运行的代理定位协程泄露的正解——
  `runtime.NumGoroutine()` 数总量分不出"在途会话"和"泄露"。测试侧同一判据见
  `test/scenario/goroutineleak_test.go`（先自证非空判据，再比对真实栈关停前后的栈增量）。
- `test/`, `tunnel/*/*_test.go` — integration tests require loopback + test certs in `test/`
- `url/` — `trojan-go://` 分享链接解析器。**只接受 `trojan-go://` scheme**（`url/share_link.go:57`），
  传 `trojan://` 会在启动期报 `url does not have a trojan-go:// scheme`（2026-10-13 实测）。
  接受的 query 键：`sni` / `type` / `host` / `path`（`type=ws` 时必填且必须以 `/` 开头）/
  `encryption`（`provider;params`，ss 走 `method:password`）/ `plugin`，fragment 作为 description。
  入口 flag 是 `-url`，配套 `-url-option`（默认 `mux=true;listen=127.0.0.1:1080`，`;` 分隔 k=v）。

## Conventions

- **Errors**: use `common.NewError(msg)` and `.Base(err)` — concatenates `" | " + err.Error()`. No `Unwrap` chain. `errors.Is`/`errors.As` DO NOT WORK across `.Base()` boundaries. 见 `common/error.go` 里的 `NewError` 与 `(*Error).Base`。
- **Panics via `Must`**: `common.Must(err)`, `common.Must2(x, err)` panic on non-nil err. 用于启动期不可恢复的接线（`common/error.go` 的 `Must`/`Must2`）。
- **Log levels are INVERTED**: `0=AllLevel, 1=InfoLevel, 2=WarnLevel, 3=ErrorLevel, 4=FatalLevel, 5=OffLevel`，另有 `TraceLevel = -1` 与 `DebugLevel == AllLevel == 0`（见 `log/log.go` 的级别常量块）。Lower = more verbose.
- **日志的凭据脱敏是"尽力而为"，不要当护栏用**（`log/log.go` 的 `ObfuscateSensitiveData`）：命中 `SensitiveKeywords`（password/token/secret/credential…）是**整行**替换成 `[REDACTED]`；只命中 `SensitivePatterns` 时**只遮命中的那段**，保留错误链上下文（旧实现连 32+ 位字母数字都要整行打码，把可诊断性一起丢了）。两个后果要记住：① 任何短于 32 字符、又不带关键词上下文的凭据**不会**被遮；② 含关键词的行整行没意义了，所以别把有用的诊断信息和 "token" 之类词写在同一条日志里。凭据要保密就**根本不要写进日志**。
- **日志调用点先判级别，再动脱敏**：`log.Debug/Info/Warn/Error`（含 `f` 变体）在清洗参数之前先过 `ShouldLog`，`Fatal/Fatalf` 不设门禁（要负责退出进程）。级别镜像存在 `log/log.go` 的 `currentLogLevel`（原子），由 `SetLogLevel` 与 `SetLogLevelInternal` 写入，`RegisterLogger` 会把它同步给新 logger。为什么这条要在乎：`SanitizeString` 是 6 条正则 + 3 次 `ReplaceAll` + `html.EscapeString`，`getLogger()` 还要抢 RWMutex 读锁，而这些过去**无论配置什么级别**都要付——`NewAddressFromHostPort` 里一句 `log.Debug` 实测 11759 ns/op、958 B/op、28 allocs/op（本机 Ryzen 5 4500U，go1.27.1）。新加/修改日志入口时照这个顺序写，别把清洗放回门禁前面；`golog` 内部还有一次自清洗，所以开启 debug 级别时是两轮，这是有意的防御而不是可以顺手去重的地方。
- **No per-instance ID.** 曾经在 `NewProxyFromConfigData` 里写入过一个 `proxyIDKey("PROXY_ID")`，但 key 类型是包内私有的——包外**无法构造**同一个 key 去读，包内也没有读者，所以它已被删除。旧文档说的“每个 creator 各自 `context.WithValue(ctx, Name+"_ID", …)`、靠它支持同 tunnel 多实例并存”**在代码里从来不存在**（没有任何 tunnel 读 `NAME_ID`）；要按会话区分实例需另想机制。
- **Context cancel is the shutdown primitive**: `proxy/proxy.go` 的 `relayConnLoop`/`relayPacketLoop` 在 select 里带上 `ctx.Done()` to exit `Accept` blocking calls. All `Tunnel.NewClient/NewServer` implementations must respect `ctx`.

## Anti-patterns (project-specific)

- **Never store the pointer to a mux header — copy it.** 见 `tunnel/mux/conn.go` 中 `ReadFrom` 循环里的原注释（“NEVER STORE THE POINTER TO HEADER, COPY THE HEADER INSTEAD”）。
- **relay 不做半关闭**：`relayConnLoop` 是「任一方向结束就拆掉整个会话」，且 `tunnel.Conn` 没有 `CloseWrite`。所以任何 tunnel 自己先产生的 EOF 会立刻终止另一方向——细节与踩坑记录见 `tunnel/AGENTS.md`。
- **`errors.Is`/`As` won't traverse `.Base()`.** Check messages or keep original error separately if you need it.
- **Don't edit `api/service/api.pb.go` or `api_grpc.pb.go`** — regenerate via `api/service/gen.sh`.
- **`SHADOWSOCKS_SF_CAPACITY="-1"` is required for tests** (disables shadowsocks stream capacity check in go-shadowsocks2). See `Makefile::test` and CI.

## Build tags

Set via `go build -tags "..."`. Selection happens entirely through `component/*.go` `//go:build` lines.

- `full` — default Makefile target; pulls everything
- `mini` — 只排除三样：`api`（gRPC 服务）、`custom`（自定义栈 run-type）与 `other`（`-url` / easy 这两个 CLI 入口，`component/other.go`）。**client / server / forward / nat 四个 run-type 以及 mysql 后端在 mini 里都在**（`component/{client,server,forward,nat,mysql}.go` 的标签都是 `xxx || full || mini`）
- `client`, `server`, `forward`, `nat`, `custom` — mode-specific subsets（`custom` 只在 `custom || full`，不进 mini）
- `api` — gRPC API server (`api || full`)
- `mysql` — 只是"额外打开" `statistic/mysql`：**`full` 和 `mini` 本来就已经包含它**（标签是 `mysql || full || mini`），所以"不加 mysql 标签就没有 MySQL 后端"的说法不成立。The sqlite persistence backend needs **no tag**: it is imported unconditionally by `statistic/memory` and gated by platform build tags inside `statistic/sqlite` (linux amd64/386/arm/arm64; other platforms get a stub that refuses to start)
- Platform: `tproxy` and `nat` proxy are Linux-only (`tunnel/tproxy/`, `proxy/nat/nat.go`)

## Commands

```
make             # CGO_ENABLED=0 go build -tags "full" -trimpath -o build/trojan-go
make test        # SHADOWSOCKS_SF_CAPACITY="-1" go test -v ./...
make test-race   # SHADOWSOCKS_SF_CAPACITY="-1" go test -count=1 -race -tags full ./...
make release     # cross-compile matrix + zip (see Makefile)
./build/trojan-go -config config.json
```

CI (`.github/workflows/`): `go.yml` is a 6-hourly dependency-maintenance job (`gofmt -r 'interface{} -> any'`, `go fix`, `go mod tidy -compat=1.27`, `gomod/gomod.sh`, then force-push); `test.yml` runs `make test` on push/PR to master (note: `make test` has no `-tags full`); `main.yml` runs the weekly cross-compile release matrix; `codeql.yml` security analysis; `docker-build.yml` image build; `dependency-review.yml` dependency review on PRs. `config-examples.yml` runs `.github/check-config-keys.py`: it parses every json/yaml tag out of the registered config structs and checks each `(section, key)` pair used in `README.md`, `docs/*.md`, `config/*.yaml` **and `example/*.yaml` + `example/*.json`** (example/ was added 2026-10-16 after `quic.enabled`—a key deleted on 2026-10-11—sat rotting in those four files; the gate already had a positive control asserting that very key must go red). Extending it to JSON goes through `pairs_from_json`; keep new example configs inside these globs or they are unvalidated. All jobs pin `runs-on: ubuntu-24.04` — do not revert to `ubuntu-latest`, which migrates to Ubuntu 26 on 2026-10-19. Actions still float on `@main`. `race.yml` is a nightly (`cron: 17 3 * * *`) + `workflow_dispatch` job running `make test-race` (whole repo under `-race`, `-tags full`). It is deliberately NOT on push/PR, so the 6-hourly dependency commits do not each trigger a ~10-minute race run. `make test` on push/PR still has no `-race`. `.github/dependabot.yml` exists but is intentionally not committed (enabling it starts opening automated PRs) — ask before adding it.

## Notes

- Upstream is archived. Fork-friendly; no contribution process.
- No `go:generate` directives anywhere. Protobuf regen is manual (`api/service/gen.sh`).
- `tunnel/tls/fingerprint/` vendors uTLS ClientHello IDs — updating uTLS may require touching this list.
- `tunnel/router/` uses v2ray's geosite/geoip data format; bundles no data — user supplies `.dat`.
- **配置解析是"同一份文档喂给所有已注册模块"**（`config.parseJSON`/`parseYAML` 遍历整个 registry）。因此只要两个模块把**同一个 JSON 路径**绑到不同类型，整份配置就会在启动时被第一个 `Unmarshal` 错误拒掉，而且报错的模块取决于用户没写的那个模块——排查起来很怪。2026-10-12 全仓核过：同名键 `alpn` / `password` / `path` / `ssl` 都分属不同路径（`quic.alpn` vs `ssl.alpn`、`mysql.password` vs 顶层 `password`、`inbound.path` vs `websocket.path`、`api.ssl` vs `ssl`），今天无碰撞；**新增模块时要照这个约束检查**（注意 `full`/`mini`/`api` 等 build tag 会改变注册了哪些模块，同一份配置在不同构建下的成败可能不同）。
- **`redirector` 的客户端地址注入由顶层开关 `redirect-client-ip`（JSON `redirect_client_ip`）控制，默认关**。2026-10-12 启用：此前 `Redirection.ClientIP` 全仓没有任何赋值点，整条注入路径不可达（文档只记录、不顺手删）。打开后 `worker()` 取 `InboundConn` 的 TCP 对端地址（调用侧显式给了 `ClientIP` 就用它），并且只有当头部块能被 `http.ReadRequest` 判成合法 HTTP 请求时，才追加 `X-Forwarded-For` / `X-Real-IP`。非 HTTP 载荷、以及读不出完整头部块（含 64KB 上限）这两条出口**原样中继并保持连接**——旧实现分别是加坏字节、以及直接关连接（对探测者自曝）。开关放在 `redirector` 自己注册的 `REDIRECTION` 配置段上，所以 trojan / shadowsocks / tls / websocket 四个入站不必各加一份键（各加一份会踩「同一路径必须同类型」那条约束）。注意这里拿到的是 TCP 对端地址：`trust-headers` 那条 `GetRealIP` 走不通，因为到重定向时认证已失败、那个 IP 还没解析出来。
- **入站层 Close 的三件必做事**：`cancel` → 关底层监听（顺序不能反，否则 `wg.Wait` 死锁）→
  `wg.Wait` → **排空投递缓冲队列**。最后这件在 `trojan`/`tls`/`transport` 早就有，其余层
  （`adapter`/`http`/`simplesocks`/`mux`/`socks`）是 2026-10-15 才补齐的：队列里每条连接都攥着一个
  已 Accept 出来的 fd，上层还没来得及 `AcceptConn` 就关停的话只能等进程退出兜底，库化/热重载场景是
  真堆积。判据形态见 `tunnel/adapter/drain_on_close_test.go`。**`quic` 是最后一个漏排空的入站**
  （2026-10-16 补上 `connChan`/`packetChan`）：它没有 per-conn fd，但队列里每条都攥着一个 stream
  或一份深度 16 的数据报缓冲加父连接，判据见 `tunnel/quic/drain_on_close_test.go`。
- **握手期挂出去的三份收尾（在途登记、时限、子 ctx）必须成对且覆盖 panic**。`tunnel/websocket`
  原先在 `websocket.Accept` 前后是三段裸顺序调用：Accept 解析的是**对端可控**的升级请求，而这条
  调用链上面 `trojan/server.go`与 `tls/server.go` 都带 recover，所以库内一旦 panic 进程照跑，
  但 `pendingConns` 表项和挂在 `s.ctx` 下的握手子 ctx 就永久留着（cancel 没人调，每命中一次多一份）。
  现在写成 `defer` + `handedOff` 标志，判据 `tunnel/websocket/handshake_panic_test.go`，注入点
  是 `var acceptHandshake`（同 `var dialUDP`，别当冗余删）。
- **`freedom` 的 forward-proxy 这一跳必须绑 `c.ctx` 并整体限时**。`router` 的 bypass 分支之外，
  `DialConn` 的 socks 分支过去直接用 `x/net/proxy` 的默认 dialer：无超时、不看 ctx，而上游 SOCKS
  代理受理 TCP 却不回握手字节是现实形态。它卡在 `dialer.Dial` 里 = 会话协程永久钉死，
  `Proxy.Close` 的 5s `waitBounded` 只是放弃等待，叫不醒它，每次撞死留一条协程 + 一个半开 socket。
  现在走 `forwardDialer`：`common.Dial(c.ctx, …)` 连代理（域名代理地址的 DNS 也一并接回 ctx）+
  握手整体截止 + `context.AfterFunc` 让 Close 当场打断阻塞中的握手；**交出隧道前必须 `disarm()`**
  （残留时限会几十秒后凭空掐断一条正常会话，`var forwardHandshakeTimeout` 就是测这个的注入点）。
- **`tproxy` 两处按对端输入增长的表都有上限了**（2026-10-16）。① 会话内回包 socket 缓存：会话按
  **源地址**复用而目的地由对端决定，旧写法只 append 不删、会话结束才统一回收 ⇒ 一条会话就是一个
  fd 放大器，现在按 `netip.AddrPort` 索引、超过 `maxReplySocksPerSession` 走 FIFO 淘汰；
  ② `mapping` 只有空闲超时，条目数 = `UDPTimeout × 新源地址到达速率`，而源地址可伪造，
  现在建会话前卡 `maxUDPSessions`（旧注释谎称"有空闲超时与源地址上限"，代码里根本没有后者）。
  判据 `tunnel/tproxy/session_bound_test.go`。同日按同一形态给 `socks`/`dokodemo` 也补上了
  `maxUDPSessions`（判据 `tunnel/socks/session_bound_test.go`、`tunnel/dokodemo/session_bound_test.go`），
  三个入站的会话表现在都有闸。**新增同类入站时照这三条抄，别只给自己那层加。**
  顺带同轮修掉 `socks` 的两处：读错误过去直接 `continue`（socket 持续报错 = 满 CPU 热循环，
  实测 400ms 内读 3500 万次；现在 100ms 退避 + 每 10 次才打一条 Error——这层不拥有
  `listenPacketConn`，是 `underlay.AcceptPacket` 给的，所以不能像 tproxy 那样自杀式关监听），
  以及拆帧闸门：`bytes.NewReader(buf[3:n])` 在 n<3 时切片越界 panic，而对端是**未认证的 UDP 源**，
  一个零长度数据报就能把进程打崩；闸门卡在 4 字节、**建会话之前**（n=3 那种不 panic 但投递不出
  负载的包过去会先攒出一条死会话），口径同 `tunnel/freedom/conn.go`。
- **只监听 `s.ctx` 的"关停唤醒 watcher"必须再给一个自身退出条件**。`tunnel/http` 的
  `closeRawConn` watcher 与 `quic` 服务端的包泵都是这个形态：只 `select { case <-s.ctx.Done() }`
  的话，每条连接留一个 park 到 `Server.Close` 的 goroutine，还经由闭包留住裸连接对象。
  修法是加一个 per-conn 的 `connDone`，在 handler 的 defer 里关掉它
  （判据 `TestOneShotConnectionsDoNotParkWatchers`，实测 40 条一次性连接 = 40 个孤儿协程）。
  注意 QUIC 服务端反过来要小心：`packetDone` 只能在**连接级**结束点亮，会话级点亮会拆掉整条连接。
- **`statistic/memory.AddUser` 必须用 `LoadOrStore`**，不能先 `Load` 判存在再 `Store`。
  窗口里两个并发调用会各自起 `speedUpdater` + `ipCleaner` 再互相覆盖，被覆盖那个 `User` 的
  `cancel` 永不被调（`DelUser` 只关 map 里那一个）→ 每撞一次永久漏 2 goroutine + 2 ticker +
  1 ctx + 1 张 ipTable。API `SetUsers(Add)` 与 mysql `syncUsers` 都会对同一 hash 撞上来。
  后台协程在登记**之前**起，抢输的那份自己 `Close()`（cancel + Wait）收尾。
- **API handler 里别留未判空的二级解引用**：`SetUsers` 原先只判 `req.Status == nil`，
  下一行就取 `req.Status.User.Hash`。protobuf 的 `Status`/`User` 是两个独立可选字段，
  而 grpc-go 默认**不** recover handler 里的 panic（不挂 `RecoveryHandler` 时直接冒到传输层），
  所以一条合法消息就能打挂整个进程。判据见 `api/service/setusers_nil_test.go`。
- **`log.ShouldLog` 把 `TraceLevel` 归到 `DebugLevel` 那一档**：级别常量越小越啰嗦，
  最啰嗦的合法配置值是 `AllLevel = 0`，而 `TraceLevel = -1`，按大小直接比会让
  `log.Trace/Tracef` 恒为 false（golog 内部其实是按 `logLevel == 0` 输出 Trace 的，
  卡的只有外层这一道门禁），router 规则命中、freedom 拨号直连、tls 握手信息因此长期静默。
  新增级别常量时记得回到这里看一眼，别靠"看起来有调用点"判断日志是否生效。
- **地址序列化只剩一条路**：生产代码里 `Address.WriteTo` / `Metadata.WriteTo` 已无调用点（只有
  `tunnel/metadata_*_test.go` 还拿它们当等价性判据），新代码请直接用 `AppendTo`。最后两个调用点
  是 2026-10-16 才拆掉的（`trojan.OutboundConn.WriteHeader` 与 `simplesocks.Conn.Write` 的出站帧头）：
  两处过去都丢弃序列化返回值，地址编不进线上格式时残缺头照样连着 payload 上线，对端状态机永久
  错位且全程无报错。"失败必须一个字都不发"的判据见 `tunnel/trojan/header_serialize_test.go` 与
  `tunnel/simplesocks/header_wire_test.go`，里面还钉住了帧的线上字节与"头与首个负载合成一次 Write"。
  注意 `Metadata.WriteTo` 还带一个 `Address.NetworkType = "tcp"` 的就地改写副作用，`AppendTo` 没有——
  全仓无人依赖它（`Address.Network()` 只被 `Metadata.Network()` 读，而后者无人调用）。
  另两处已核实但**没动**的热点，改之前先掂量：① `tunnel/tproxy/udp.go` 的 `DialUDP` 是**每个回包**
  新建 socket + `os.NewFile`，按源地址缓存连接能省掉大量 fd  churn，但要引入 TTL 与 NAT 语义；
  ② socks/tproxy/dokodemo 的 UDP 会话表以 `addr.String()` 为键，每包一次格式化，换成
  `netip.AddrPort` 能省掉，但 IPv4-in-IPv6 与 v6 zone 的键相等语义会改变现有会话归属，
  必须先有判据再动。
- **`recorder.Add` 在零订阅者时直接返回**（`subscriberCount` 短路）。它是每连接、每 UDP 包都会
  被 `tunnel/trojan/packet.go` 调到的路径，旧实现先付 2 次 `addr.String()` + 2 次 `SplitHostPort`
  + 时间戳才发现在 range 空表。计数由 `Swap`/`LoadAndDelete` 的返回值维护，同 uid 重复
  Subscribe 不会重复计数。
- **tproxy 的回包 socket 按目的地缓存在会话上**（`tunnel/tproxy/server.go` 的会话守护协程）。
  旧写法是**每个出向包**一次 `DialUDP`：`socket()+IP_TRANSPARENT+bind+connect+os.NewFile+FileConn`
  再立刻 `Close`，一条会话几十包就是几十套这套调用。注意 `DialUDP(network, laddr, raddr)` 的参数
  次序——`laddr` 是**伪造的原始目的地**（TPROXY 语义要绑上去，客户端才分不出代理），`raddr` 才是
  客户端；所以复用键取 metadata 里的目的地 IP:port，socket 存活期跟随会话（会话表有空闲超时与
  源地址上限（`maxUDPSessions`），不会无界增长）。这条路径靠 `var dialUDP = DialUDP` 这层注入点做判据
  （真实 `DialUDP` 需要 CAP_NET_ADMIN，CI 与本机都起不来），**别把它当冗余代码删掉**。
- `common/geodata.EmitBytes` 的读取长度来自 `.dat` 里的 varint，**分配前必须用文件剩余长度卡住**
  （`ErrInvalidGeodataLength`）。损坏的 geoip.dat 曾直接 `make([]byte, 1<<60)` ⇒
  `makeslice: len out of range` panic，而 router 是在启动时加载它的。改这里时别退回"一次 `f.Read`
  读不满就报硬错误"：`io.Reader` 允许短读，必须用 `io.ReadFull`。
- **入站串行 AcceptConn 的握手窗口必须双向限时**。`tunnel/websocket` 的 `AcceptConn` 直接被入站
  accept 循环串行调用，卡住它等于这个入站不再接纳任何新连接。旧实现只给"读升级请求"设了
  `SetReadDeadline`，读完就把写方向裸交出去，而 `websocket.Accept` 会同步 flush 101 响应——对端把
  请求发完就不再读取（TCP 零窗口）时这次写永久阻塞，且这条连接早已离开下层的待处理队列，排空逻辑
  够不着它的 fd。修法是 `conn.SetDeadline(handshakeTimeout)` 覆盖整个握手窗口、移交上游前解除，
  再加 `pendingConns` 在途登记让 `Close` 立刻关掉它（`tunnel/tls/server.go` 早就是这套形状）。
  判据：`tunnel/websocket/handshake_stall_test.go`，用 `net.Pipe` 造"读完请求就不再读"的对端，
  在白盒包装连接上盯 `Write` 被调用那一刻有没有落在有效写时限内。
- **Markdown 站内锚点由 `.github/check-md-anchors.py` 把关**（挂在 config-examples 工作流）。
  GitHub 的 slug 规则：小写、丢掉标点/符号/控制字符（**下划线保留**，所以
  `#中继缓冲区-relay_buffer_` 是活的）、空格换连字符，标题里的序号也会进锚点
  （`### 3. URL 模式（客户端）` → `#3-url-模式客户端`）。带空格的 `(#cdn 中转配置)` 是死链，
  页面不报错、测试不变红，能活到下次改版。门还要抓"括号丢了"的畸形链接——那种写法根本不构成
  链接，只查锚点是否存在会把它当成没有链接而放过（我自己踩过：批量替换把 `(...)` 一起吃掉，
  门照样 PASS）。
- **README 里的 ```yaml 代码块是会被实喂给二进制的**（`.github/extract-doc-yaml.py` 只取
  ```yaml 围栏，```json 块一律漏过）。所以文档里的示例配置尽量写成 yaml：写 JSON 等于放在
  没有任何门检查的散文里。CDN 一节已改成 yaml，并纠正了三处与代码不符的说法：服务端栈是
  `transport → tls → websocket → shadowsocks → trojan`，**只有 `transport-plugin` 开启才跳过
  `tls`**（`proxy/server/server.go`），因此 CDN 回源这一跳必须仍是 TLS 或 L4 直通；`local-port`
  不能和同机 nginx 抢 443；服务端 `websocket.host` 不参与任何校验（`InsecureSkipVerify`），
  只在 `remote-addr` 留空时充当诱饵地址兜底。
- **coder/websocket 的 `Conn.Close` 会发 close 帧并等对端回执**，哑对端不回就白等满库内写超时
  （实测 5s）。这是测试夹具现象，不是生产泄露：真实对端回 close 帧，`NetConn` 的读上限与定时器
  都在 `Close` 里停掉。写测试时要让对端先撤、或持续读到 EOF，再关 ws 连接，否则单用例白加 5 秒。

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