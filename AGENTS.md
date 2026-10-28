<!-- Generated: 2026-04-20 | Commit: 02d5c12 | Branch: latest -->

# trojan-go

Go implementation of the Trojan proxy protocol. Unmaintained upstream (archived), but still built/released via CI. Module: `github.com/Potterli20/trojan-go-fork`, `go 1.27.3`（go.mod 与 CI 的 `^1.27` 一致），CGO disabled by default.

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

- **Errors**: use `common.NewError(msg)` and `.Base(err)` — concatenates `" | " + err.Error()`. `(*Error).Unwrap` **现在有**（`common/error.go`），所以 `errors.Is`/`errors.As` **能**穿过 `.Base()` 边界（实测：`errors.Is(NewError("outer").Base(sentinel), sentinel)` 为 true，而 `Error()` 仍是 `"outer | sentinel"`）。旧文档写的"没有 Unwrap 链、Is/As 不工作"是改造前的状态，已不成立——但别由此把 `option.IsNotSet` 换成 `errors.Is`：它比的是错误文本（见 `option/AGENTS.md`）。
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
  原先在 `websocket.Accept` 前后是三段裸顺序调用：Accept 解析的是**对端可控**的升级请求，一旦库内
  panic，`pendingConns` 表项和挂在 `s.ctx` 下的握手子 ctx 就永久留着（cancel 没人调，每命中一次多
  一份）。旧文档在这里写过"调用链上面 `trojan/server.go` 与 `tls/server.go` 都带 recover，进程照跑"
  ——**那句与代码不符**：那两处 recover 在 per-conn handler 的 goroutine 里，而 AcceptConn 是在
  accept **循环体**里被串行调用的，panic 会冒出循环把进程带走；2026-10-17 由
  `tunnel.AcceptConnGuarded` 补上这道边界（见下面那条）。
  现在写成 `defer` + `handedOff` 标志，判据 `tunnel/websocket/handshake_panic_test.go`，注入点
  是 `var acceptHandshake`（同 `var dialUDP`，别当冗余删）。
- **入站的串行嗅探必须挪进 per-conn 协程**（2026-10-20）。`tunnel/adapter` 的 `acceptConnLoop`
  过去在循环体里读前 3 字节判 socks/http，一条只连接不发数据的对端就能让整个入口停摆满
  `firstByteTimeout(10s)`，慢速扫描即可让 socks/http 不可用。现在循环只做 `Accept`，嗅探交给
  `s.wg.Go(sniffConn)`；判据 `tunnel/adapter/sniff_parallel_test.go`（先挂静默连接、再发完整
  SOCKS5 握手，必须在 2s 内被接走；退回串行立刻红）。
- **`transport` 嗅探失败后的去向要看有没有直接消费者**（2026-10-20）。明文插件 + websocket 的栈是
  `transport → websocket → trojan`，`connChan` 没有消费者，旧实现把非 HTTP 连接一律投进
  `connChan`，32 条填满后每个新连接都卡在发送上（协程 + fd 随扫描流量堆积）。现在 `AcceptConn`
  按 overlay 登记 `directConsumer`（tls/trojan 消费时置位）：有就直接投 `connChan`，没有就交给
  `wsChan` 让 websocket 重放后走诱饵重定向。判据 `tunnel/transport/fallback_route_test.go`
  （无直接消费者 → wsChan；有 → connChan，正反两条）。
- **accept 路径必须有 panic 边界，而且被收住之后要当成"这一层还活着"**（2026-10-17）。全仓原本只有
  mux/tls/trojan 三处 `recover()`，且都在 **per-conn handler 的 goroutine 里**；各入站的 `acceptLoop`
  却把 `s.underlay.AcceptConn(&Tunnel{})` 写在**循环体**里，而下层解析的是**未认证对端可控字节**
  （websocket 的 `http.ReadRequest`+`Accept`、shadowsocks 的密文探测、simplesocks 的 `readHeader`），
  panic 一路冒出 accept 协程、main 又没有顶层 recover ⇒ 命中一次整个进程没了。现在统一走
  `tunnel.AcceptConnGuarded`（`tunnel/accept_guard.go`）：panic 变成以 `tunnel.ErrAcceptPanic` 包装的
  error，六个循环（tls/trojan/socks/http/mux/simplesocks）在错误分支**先认这个哨兵**，认出来就
  `log.Error` + `continue`。为什么不能沿用既有的"accept 出错就 return"：那样一次 panic 就把这个入站
  **永久停摆**——没有退出码、没有重启、只是再也不接纳新连接，比崩了更难发现。panic 时的**资源**收尾
  归抛 panic 的那一层（栈展开时它的 defer 跑完）：websocket 有**两条** defer——`connHandedOff` 负责关
  连接（2026-10-28 才补上；此前只有 `handedOff` 取消握手子 ctx，旧文档把它写成"资源收尾"是与代码不符的）、
  shadowsocks 的 `takenOver`
  （重定向那条路径 `result` 也是 nil，绝不能顺手关掉正在被拷贝的连接）、以及 simplesocks 的读头——
  它**在自己为超时单起的那个 goroutine 里**，父循环这道边界够不着，是实测出来的（加之前测试二进制
  当场 panic 退出）。判据 `tunnel/accept_guard_test.go` 与 `tunnel/simplesocks/accept_panic_test.go`
  （后者带正向对照：把循环退回裸调用立刻崩二进制）。**新增入站照这六处抄，并在自己的 AcceptConn
  里为对端字节留好资源收尾。**
- **`freedom` 的 forward-proxy 这一跳必须绑 `c.ctx` 并整体限时**。`router` 的 bypass 分支之外，
  `DialConn` 的 socks 分支过去直接用 `x/net/proxy` 的默认 dialer：无超时、不看 ctx，而上游 SOCKS
  代理受理 TCP 却不回握手字节是现实形态。它卡在 `dialer.Dial` 里 = 会话协程永久钉死，
  `Proxy.Close` 的 5s `waitBounded` 只是放弃等待，叫不醒它，每次撞死留一条协程 + 一个半开 socket。
  现在走 `forwardDialer`：`common.Dial(c.ctx, …)` 连代理（域名代理地址的 DNS 也一并接回 ctx）+
  握手整体截止 + `context.AfterFunc` 让 Close 当场打断阻塞中的握手；**交出隧道前必须 `disarm()`**
  （残留时限会几十秒后凭空掐断一条正常会话，`var forwardHandshakeTimeout` 就是测这个的注入点）。
  `common.Dial` 的重试间隔也已接 ctx（timer+select 取代裸 `Sleep`，2026-10-20）：否则取消后
  最多还要白等一个 `RetryInterval`，期间调用方卡在关停里。判据
  `common/net_test.go` 的 `TestDialRetryWaitHonorsContext`（ctx 已取消 + 3×5s 间隔必须立即返回）。
- **UDP 负载缓冲在三层入站都池化了，归还责任跟着借用权走**（2026-10-16，`socks`/`dokodemo`/
  `tproxy`）。口径抄 `tunnel/router`：Get 时计数加一，Put 时过 `cap` 闸门（超尺寸或根本不是池
  对象就丢给 GC，不顶高水位）再减一。归还点必须覆盖：消费端（守护协程写完、`ReadWithMetadata`
  复制完）、**所有丢包出口**（投递 `default:`、地址不可表示、会话上限拒绝）、序列化/写失败的提前
  `return`（手上那条不在任何队列里，`Close` 的排空够不着它），以及会话 `Close` 的排空。
  判据是**账目**而不是"有没有复用"：`pool_account_test.go` 要求一轮突发 + 拆会话之后
  `borrowedPacketBufs()` 回到起点，为负即双归还——那比漏归还严重得多，两个包会共享同一块
  底层数组、互相串数据。刻意不用"Get 之后有没有新建对象"来判：`sync.Pool` 的本地队列会在
  P 之间被窃取，`-race` 下有假失效（见 trojan 的 `TestMaxSizePacketFitsPooledCapacity` 注释）。
  实测 tproxy 写方向 551.7 ns/op、1440 B/op → 173.2 ns/op、56 B/op（`BenchmarkWriteWithMetadata`，
  go1.27.1，本机）。超尺寸负载刻意不截断：走一次性分配，靠 Put 的 cap 闸门自动排除在池外。
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
- **`socks` 的第三张表也有闸了**（2026-10-17）。`associations` 登记的是**已握手的 ASSOCIATE
  控制连接**，过去只有 `associationMaxLife`(10min) 一个时限，条目数 = 发呆对端的到达速率 × 600s，
  每条攥着 1 个 fd + 1 个守护协程 + 1 个 `AfterFunc` 计时器——正是上一条「新增同类表照这三条抄」
  漏掉的姊妹表（这条 TCP 在握手阶段没有任何认证）。`maxAssociations` 取与 `maxUDPSessions` 同值：
  控制连接再多也开不出更多 UDP 会话。**容量检查与登记在同一把写锁里，调用点在回成功响应之前**，
  所以被拒的客户端收到的是 `REP=0x01` 定长 10 字节失败帧（`declineAssociate`），而不是"答应了
  又默默断线"；短于 10 字节会被标准客户端读成协议错误。判据
  `tunnel/socks/association_bound_test.go`（正向对照：把闸放开一格 `TestAssociationCountIsBounded`
  立刻红）。
- **回包组帧缓冲必须按最坏合法帧留 headroom**（2026-10-17）。`socks` 会话守护协程的 `responseBuf`
  与 `freedom` 的 `socksPacketBufPool` 此前都只留 `MaxPacketSize`，而一帧是「RSV+FRAG(3) +
  地址块(ATYP+长度字节+255 域名+端口 = 259) + 负载(≤8192)」= 8454 字节（原文写的 10454 是算错的，2026-10-27 复核：3+259+8192=8454，代码里的 +264 headroom 是对的） ⇒ 满尺寸负载每包把
  `append` 撑到界外、新造一份 10880（Go 的 size class），缓冲本身却永远停在 8192，下一包再撑一次；
  远端按线上格式合法发满尺寸 datagram 就能逐包加压本地 GC。现在两处都留 `+264`
  （`responseFrameCap` / `socksFrameBufCap`），口径同 trojan 的 `packetBufCap`；`freedom` 读侧显式
  `ReadFrom(buf[:MaxPacketSize])`，不让这点 headroom 改动内核在这一跳截断的语义。判据钉的是**确定性
  的容量事实**（`TestMaxSizeResponseFrameFitsReservedBuffer`、`TestMaxSizeSocksFrameFitsPooledCapacity`），
  不用"再取一次池看有没有新建对象"，理由同 trojan 那条 `-race` 下假失效的注释。
  顺带：`tunnel/socks`/`dokodemo`/`tproxy` 三个入站打到 `maxUDPSessions` 之后原来**每个被丢的包**
  都记一条 `log.Error` 并顺带 `addr.String()`，而源地址可伪造 ⇒ 日志速率就等于攻击速率，且默认级别下
  格式化和整条脱敏链照样付（门禁在 `log` 函数内部，参数在它之前就已算完）。现在每 10 次一条 Error，
  其余走 `log.ShouldLog` 之后才格式化；同轮把 `tunnel/quic/congestion.go` 的 12 处
  `log.Debug(fmt.Sprintf(...))` 改成门后的 `Debugf`/`Warnf`。
- **只监听 `s.ctx` 的"关停唤醒 watcher"必须再给一个自身退出条件**。`tunnel/http` 的
  `closeRawConn` watcher 与 `quic` 服务端的包泵都是这个形态：只 `select { case <-s.ctx.Done() }`
  的话，每条连接留一个 park 到 `Server.Close` 的 goroutine，还经由闭包留住裸连接对象。
  修法是加一个 per-conn 的 `connDone`，在 handler 的 defer 里关掉它
  （判据 `TestOneShotConnectionsDoNotParkWatchers`，实测 40 条一次性连接 = 40 个孤儿协程）。
  注意 QUIC 服务端反过来要小心：`packetDone` 只能在**连接级**结束点亮，会话级点亮会拆掉整条连接。
- **到期与额度是「认证时」判定，不是「同步轮」判定**（2026-10-27，issue #102）。持久化后端把
  `expire`（Unix 秒，`0`/负 = 永不）与 `quota`+库侧已用量推进内存用户（`memory.SetUserExpire` /
  `SetUserQuota`，刻意**不**回写 persistencer——这些值本来就是后端每轮读出来的事实），
  `memory.AuthUser` 用 `isExpired || isOverQuota` 当场拒绝。额度判定用「库侧基线 + 内存里尚未
  刷库的增量」：只看库里的 `download+upload` 恰好漏掉整个 `check_rate` 窗口。两条推论要记住：
  ① `quotaChecked` 必须是独立的 `atomic.Bool`，不能拿 `quotaLimit` 的零值当「没有额度概念」——
  MySQL 语义里 `0 = 无额度`，零值缺省会把内存用户和 SQLite 用户全部拒掉；② 三条「库里不该再有
  这个用户」的出口（到期 / 超额 / 整行被删）必须**先补一次写回再删**，因为 `DelUser` 会
  `User.Close()`，而 `Close` 里的 `ResetTraffic` 只清零、不写库。
  同轮的 MySQL 改动：`usersTableDDL` 的 `quota` 默认值取 `-1` 而不是 `0`（`DEFAULT 0` 会让面板只填
  `(username, password)` 的新用户在首个同步轮被静默删掉，密码永远不通且不报任何错）；连接参数改用
  `mysql.NewConfig` + `NewConnector`，于是自带转义（手拼 DSN 时口令里的 `?` 会被驱动按第一个 `?`
  切进参数段，IPv6 主机拼成 `tcp(::1:3306)`）、有 `Timeout`/`ReadTimeout`/`WriteTimeout`（没有它们时
  MySQL 半开会永久卡住每轮查询，用户增减 / `quota` / `expire` / 流量写回整体静默停摆，因为那个 ctx
  只在关停时取消）、并且 TLS 随连接器走而不写 `RegisterTLSConfig` 那张按名字覆盖的全局表。
  `statistic/mysql` 此前零测试，注入点是新增的 `mysqlDB` 接口（真实 `*sql.DB` 经 `sqlBackend`
  满足）；判据见 `sync_users_test.go` 与 `dsn_config_test.go`。
- **限速器必须按 burst 拆分等待**（2026-10-27）。`rate.Limiter.WaitN(ctx, n)` 在 `n > burst`
  时**立即返回错误、而不是等待**，而 `burst = 2*limit`、中继每块最大能到 `relay_buffer_size`
  （默认 8192，上限 1MB）——所以 `speed_limit` 低于 4096 B/s 的用户过去**完全不设限**，错误还被
  调用点丢掉。`memory.waitPaced` 把 `n` 拆成不超过 burst 的若干次等待。判据用 Go 1.27 的
  `testing/synctest` 假时钟（`limiter_pace_test.go`）：气泡内时间只在所有 goroutine 都
  durably blocked 时才前进，所以「等了多久」是精确读数，不吃墙钟也不 flaky——这条是本项目里
  计时类判据的正确形态。
- **空数据报不是会话结束**（2026-10-27）。`proxy.relayPacketLoop` 两个方向过去把
  `n == 0 && err == nil` 当 EOF，`closeDone()` 拆掉整条 UDP 会话且一行日志都不打。
  `dokodemo`/`socks`/`tproxy` 的 `copy(p, payload)` 在负载为空时返回 `(0,nil)`，
  `trojan/packet.go` 在远端写 `length=0` 时同样返回 `(0,nil)`——本地任意 app 发一个空
  datagram 就能自拆会话，客户端模式下远端还能逐包拆。现在是 `continue`（口径同
  `tunnel/quic` 那条「零长度数据报绝不上交」）。判据 `proxy/packet_empty_datagram_test.go`
  用通道做 happens-before，不等墙钟。
- **日志实参的清洗必须覆盖 `error`**（2026-10-27）。`log.SanitizeLogInput` 过去只重写
  `string` 实参，而全仓主流写法是 `log.Error(common.NewError(...).Base(err))`——`*common.Error`
  不是 string，原样穿过 `fmt.Sprintln` 落到 `golog` 的行缓冲（`Output` 只 `Append`，不二次清洗）。
  后果是 CR/LF 可伪造整行日志（域名/SNI/目的地是 `metadata.go` 里长度前缀的任意字节，
  `freedom` 的拨号错误又会把 `addr.String()` 拼进文案），并且凭据脱敏链一起被绕过。
  判据 `log/log_injection_test.go`。
- **「接受但不生效」和「退化成连本机」都算缺陷**（2026-10-27）。本轮三处同一味道：
  ① trojan / websocket 服务端在 `remote-addr` 为空时不再继续启动——空主机会拼出 `":port"`，
  而实测 `net.Dial("tcp", ":port")` **连的是 127.0.0.1**，认证失败的流量被代打到代理自己
  主机的本地服务；`disable-http-check: true` 时连那道探测都不跑，全程静默。口径抄
  `shadowsocks`/`quic`（它们早就显式拒绝空 RemoteHost）。注意这道闸必须排在
  `api.CheckExposure` **之后**：`api/service/wiring_test.go` 钉的是"无凭据公网监听"那条
  文案优先，排前面会把既有断言改红（踩过）。
  ② quic 的 `congestion: brutal` 缺 `brutal-up`/`brutal-down` 时改为建栈失败——过去只 Warn，
  连接照常用库默认 BBR 上线，用户以为链路被固定码率整形（brutal 的唯一理由）实际没有。
  ③ `-url-option` 的键集合按 `Handle` 真正读取的 `listen`/`mux`/`api` 校验，未知键报错；
  尾随 `;`/`;;` 只当空段跳过（一个书写习惯不该判死一次可用启动）。
  另有一处判据教训：h2 的目标站状态码钳制**做不出端到端判据**——去掉钳制后
  `httptest.ResponseRecorder` 与错误分支都给出 5xx 形状的出口，端到端断言恒真
  （变异测试报绿才发现）。改成纯函数 `clampOriginStatusCode` + 一条接线用的结构性门禁，
  并把"为什么不能端到端"写进注释。实测 stdlib 的 `http.ReadResponse` 接受
  `HTTP/1.1 000 x`（StatusCode=0）与 `099`（99），只有四位数才被拒——所以 x/net 的
  `checkWriteHeaderCode` 对 `code < 100` 的 panic 是真可达路径。
- **`curve_preferences` 的名字表两侧必须同集合，未知名字拒绝启动**（2026-10-27）。
  `fingerprint.ParseCurvePreferences` 与 `ParseUTLSCurvePreferences` 过去对认不出的名字直接
  `continue`：拼错的 `X25519MLKEM768`（少个 8）、或只在 stdlib 侧有名字（Go 1.27 新加的
  `MLKEM1024`，码点 514；utls 那个 pin 也已定义）都会被无声丢掉——用户以为密钥交换换成了
  后量子，实际发的还是默认集合，而握手照样成功、毫无症状；带 fingerprint 与不带走标准库
  两条路还会发出**不同**的集合。现在 `ValidateCurvePreferences` 由 tls/quic 四个建栈点同步
  调用，并钉住两侧码点一致（判据 `curve_prefs_test.go`，其中"两侧名称集合必须相同"这条
  直接复现了本轮发现的缺口）。
- **不采纳 `encoding/json/v2`（已实测，别再重复评估）**（2026-10-27）。Go 1.27 把
  `encoding/json/v2` 与 `jsontext` 放进 stdlib 且**不需要 GOEXPERIMENT** 就能编译运行，
  但这里没有它的用武之地：① 本项目的 JSON 只在启动时解析一次配置，API 走 protobuf，
  没有 JSON 热路径；本机对 `example/server.json`（1948 B）两轮 20000 次解析实测
  v2 比 v1 **慢 6%~33%**（噪声大，方向一致：没有性能可赚）。② v2 默认**区分大小写**匹配
  字段名，v1 不区分——实测同一个 `{"PASSWORD":"x"}`：v1 命中 `password` 字段并报类型错误，
  v2 直接忽略该键返回 nil。叠加"同一份文档喂给所有已注册模块"这条既有特性，
  切换会把今天能用的写法变成静默失效。③ 要保住兼容就得把 legacy 选项全开，
  那就只剩风险没有收益。结论：配置解析继续用 `encoding/json`（v1）。
- **`statistic/memory.AddUser` 必须用 `LoadOrStore`**，不能先 `Load` 判存在再 `Store`。
  窗口里两个并发调用会各自起 `speedUpdater` + `ipCleaner` 再互相覆盖，被覆盖那个 `User` 的
  `cancel` 永不被调（`DelUser` 只关 map 里那一个）→ 每撞一次永久漏 2 goroutine + 2 ticker +
  1 ctx + 1 张 ipTable。API `SetUsers(Add)` 与 mysql `syncUsers` 都会对同一 hash 撞上来。
  后台协程在登记**之前**起，抢输的那份自己 `Close()`（cancel + Wait）收尾。
- **gRPC API 默认失败关闭**（2026-10-16）：`api.enabled` 且监听地址非回环（`api_addr` 留空
  就是通配）时，必须配 `api.token` 或 `api.ssl.enabled`+`verify_client`，否则**拒绝启动**；
  `api.allow_insecure: true` 是显式逃生阀。关键是闸门要由 `tunnel/trojan.NewServer`/`NewClient`
  在**任何资源分配之前**同步调用（`api.CheckExposure`，检查器由 `api/service/auth.go` 的 `init`
  注册）：`api.RunService` 的返回值在两个调用点都被丢进 goroutine，在那里 return error 等于
  什么都没发生——代理照常跑、API 悄悄不在、日志一行都没有。细节与判据见
  `api/service/AGENTS.md` 的 Authentication 一节。
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
  另一处已核实但**没动**的热点：`tunnel/tproxy/udp.go` 的 `DialUDP` 是**每个回包**
  新建 socket + `os.NewFile`，按源地址缓存连接能省掉大量 fd churn，但要引入 TTL 与 NAT 语义
  （会话内按目的地缓存那半已经做了，见下面那条 bullet）。
- **trojan 出站的首个负载必须计入流量**（2026-10-20）。`WriteHeader` 把帧头与首个 payload
  合成**一次** Write（见上面的地址序列化条目），`OutboundConn.Write` 在 `written` 分支若直接
  `return len(p)`，每条连接的首块（最多一个 relay buffer，8KB）就不进用户流量/限速与 tracker
  的 sent——上行统计系统性偏低。判据 `tunnel/trojan/traffic_accounting_test.go`：首块与第二块
  合计必须等于 `GetTraffic()` 的 sent。
- **UDP 会话表的键已经是 `tunnel.SessionKey`**（2026-10-16，`tunnel/session_key.go`）。
  原先 socks/tproxy/dokodemo 三层都拿 `addr.String()` 当键，那是**每个 UDP 包**一次 IP
  格式化加一次字符串哈希；现在 `*net.UDPAddr` 走 `AddrPort()` + `Unmap()`，不碰字符串。
  换键的前提是"哪些地址算同一条会话"不能变，两条不那么显然的规则由
  `tunnel/udp_session_key_test.go` 钉住：① v4 与 v4-mapped-v6 **同键**（双栈 socket 上收到
  IPv4 对端给的是 4-in-6 形态，不 `Unmap()` 就会把同一台客户端拆成两条会话——这正是
  `String()` 一直免费替我们做的事）；② zone **是**键的一部分（实测 go1.27.1 的
  `(*net.UDPAddr).AddrPort()` 会保住 zone，写判据之前我以为它会丢）。解析不出 host:port 的
  未知 `net.Addr` 实现退回字符串键，行为与改造前逐字一致。**新写入站会话表直接用这个类型，
  别再自己拼字符串键。**
- **三处"看着能省"的分配，量过之后的结论是别动**（2026-10-16，go1.27.1，本机 Ryzen 5 4500U）：
  ① `Address.ReadFrom` 域名分支的 `make([]byte, length+2)` 换成栈上 `var scratch [257]byte`
  **更差**：172.2 ns/op、120 B/op → 242.3 ns/op、384 B/op（分配次数都是 6）。原因是
  `io.ReadFull(r, buf)` 是接口方法调用，编译器无法证明实现不留引用，于是整个 257 字节数组
  逃逸到堆上，比按需分配的 17 字节大一个数量级。**栈数组只在"不把切片交出去"时才是零分配。**
  数字与复现见 `tunnel/metadata_parse_bench_test.go`。
  ② `simplesocks.Conn.Write` 与 `trojan.OutboundConn.WriteHeader` 的首帧拼接缓冲已经是
  1 alloc/op（300 字节负载实测 151.3 ns/op、384 B/op，`tunnel/simplesocks/first_frame_bench_test.go`），
  而且是**每条 mux 流**一次、不是每包一次；再套一层池要引入借用/归还、cap 闸门与拆流排空，
  换来的是每条流省一次分配——不划算。
  ③ `tunnel/mux/conn.go` 的 SYN/FIN 帧头 `make([]byte, 8)`：这两个 8 字节对象**必须**是堆上副本
  （要进 `synQueue`/`finQueue`，而 smux 会复用它自己的头缓冲——就是那句 NEVER STORE THE
  POINTER TO HEADER），且 `pushFrameHeader` 在队列满时会就地发出，池化得在两条出口上都归还。
  每条流 2 次 8 字节分配，不值这个复杂度。
- **`Write` / `WriteWithMetadata` 返回之后不得再碰调用方的切片**（跨层契约，写在
  `tunnel/tunnel.go` 的接口注释上）。`proxy/proxy.go` 的 `relayConnLoop` 把
  `boundedBufPool` 的 buffer 喂给 `io.CopyBuffer`、`relayPacketLoop` 则是
  `WriteWithMetadata(buf[:n], m)` 一返回就 `packetPool.Put(buf)`，下一个包大概率拿到
  同一块底层数组。所以"存引用、晚点发"这类写法不会当场出错——出错是在包与包之间把
  别人的负载串进来。判据：`tunnel/socks/no_retain_test.go`、
  `tunnel/dokodemo/no_retain_test.go`、`tunnel/trojan/packet_write_test.go`（涂脏 p 再核对
  上线/入队的字节；把 socks 的 `copy` 删掉，三条里对应那条立刻红）。
  `ReadWithMetadata` 交回的 `*Metadata` 同理，不得引用那块 payload buffer
  （`tunnel/freedom/pool_alias_test.go` 钉的就是这条）。
- **`recorder.Add` 在零订阅者时直接返回**（`subscriberCount` 短路）。它是每连接、每 UDP 包都会
  被 `tunnel/trojan/packet.go` 调到的路径，旧实现先付 2 次 `addr.String()` + 2 次 `SplitHostPort`
  + 时间戳才发现在 range 空表。计数由 `LoadOrStore`/`LoadAndDelete` 的返回值维护。**同一 uid
  重复 `Subscribe` 返回错误**（2026-10-20）：旧实现用 `Swap` 就地替换表项，旧通道再没有任何人
  关闭，原调用方永久阻塞在接收上；判据 `recorder/duplicate_subscribe_test.go`（退回 Swap 立刻红）。
  **数量也有上限了**（`maxSubscribers=64`，`Subscribe` 超限时返回错误
  而不是给一条永远收不到记录的通道）：`GetRecords` 每条流现生成一个 UUID，条目数等于并发
  尾随流数，而单订阅者的缓冲上限只管得住"一个"。判据 `recorder/subscriber_limit_test.go`，
  里面还钉住上限值本身（调高就要先重算最坏驻留）。
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
- **h2c 关停用例的口径：502 是正确出口，不是失败**（2026-10-17）。`TestH2CServerCloseWakesInflightStream`
  原先断言"客户端必须拿到 error"，于是把 `h2.go` 失败出口里那句 `http.Error(w, "bad gateway", 502)`
  判成了红——而它到底能不能刷出去，取决于 502 有没有赶在连接拆掉之前写完，快机器上随机失败
  （在干净 HEAD 的独立 worktree 上复现过：3 次里红 1 次）。现在只钉两件真的不变量：**有限时间内
  结束**（Close 的耗时断言 + 客户端 3s 兜底）与**不编造成功响应**（`err == nil && status < 500` 才算红）。
  正向对照：让夹具真的回一个 200，断言立刻红。写这一类"关停时在途"的用例照这个口径，别把
  "传输错误"当成唯一可接受的结果。
- **h2 的请求体泵失败时必须关 `reqWriter`**（2026-10-20）。`h2Handler.ServeHTTP` 为流式请求体
  单起一个 goroutine 跑 `r.Write(reqWriter)`，旧实现出错只打日志：客户端 RST_STREAM/断连或读
  请求体失败后，relay 的 `OtherConn.Read` 永久堵在 `reqReader.Read`，而 `ServeHTTP` 本身又堵在
  `http.ReadResponse` 上，`defer stream.Close()` 永远轮不到 ⇒ 每条被放弃的流留两个中继协程 +
  两组 `io.Pipe` + 一个 ctx 到 Server 关停。修法是错误分支 `reqWriter.CloseWithError(err)`：
  **只关写端，成功路径绝不能提前发 EOF**（relay「任一方向结束就拆会话」会砍断在途响应，h1 那侧
  同理）。判据 `tunnel/http/h2_body_leak_test.go`：直接构造 `h2Handler`，请求体读立即报错，relay
  侧循环 `Read` 必须在 3s 内拿到错误；去掉那句 Close 实测在 3.00s 处超时变红。注意泵在失败前
  已经把请求头写进管道，单次 `Read` 会先拿到 nil error 的头部字节，判据必须读到错误为止。
- **`DialConn` 与 `DialPacket` 是两条独立的路，修了一条别忘了姊妹**（2026-10-19）。forwardDialer
  当年只覆盖了 `freedom.DialConn`，`DialPacket` 的 SOCKS5 UDP ASSOCIATE 分支仍旧把整段握手交给
  `socks5-fork` 自己：`NewClient(..., tcpTimeout, udpTimeout)` 传的是 `0, 0`，而库只在
  `TCPTimeout != 0` 时才 `SetDeadline`——上游代理受理 TCP 却不回握手字节时 `Negotiate`/`Request`
  里的读永不返回，调用方是 relay 的会话协程，一次撞死永久占用一条协程加一个半开 socket，
  `Client.Close` 只 cancel ctx 而这个拨号从头到尾不看 ctx。现在走 `socksAssociateTimeout`
  （判据 `tunnel/freedom/udp_associate_test.go`：静默代理必须在几秒内带错误返回；正向对照是把
  时限退回 0，那条用例会挂在 10s 兜底上判死；另有一条正常应答的假代理钉住"加了截止不许掐断
  成功链路"）。两个坑记下来：① 库的超时粒度是**整秒**，所以 `socksTimeoutSeconds` 必须**向上**
  取整——向下取整会把测试缩小的 300ms 变成 0，而 0 在库里恰好是"不设截止"，判据会静默变绿；
  ② **连接这一跳仍然接不回 ctx**：`socks5.DialTCP` 是包级 var（无 ctx），freedom 是全仓唯一
  调用方，改它等于 monkey-patch 一个全局量（`DialPacket` 是并发调用的，存"当前 ctx"会互相踩）。
  所以黑洞型代理（SYN 丢弃）最坏仍要挂满内核那约 2 分钟才失败——有界、能自愈，但不是即时取消。
  要拿到即时取消得给上游 fork 加 ctx 化的拨号注入点，那是跨仓库改动，先问再做。
- **启动期的探测拨号也要接 ctx**（2026-10-19）。`trojan.NewServer` 的诱饵探测与 `tls.NewServer`
  的 fallback 探测原本是裸 `net.Dial("tcp", …)`：无超时、不看 ctx，而它们在 `NewServer` 里是
  **同步**调用——诱饵地址丢 SYN 时建栈主协程挂约 2 分钟，期间 Ctrl-C 与上层 cancel 全都无效
  （域名还叠一轮不看 ctx 的 DNS）。同仓 `redirector.defaultDial` 早就改成 30s 超时了，这两处是漏网。
  判据的形态值得照抄：不要去构造"连不上"（黑洞地址在 CI 上不可移植），而是拿一个**活着**的监听
  当诱饵、配一个**已取消**的 ctx——不看 ctx 的旧实现照样连上并把探测判成通过，看了 ctx 的实现
  必须立刻失败且错误文案指向这道探测（`strings.Contains(err, "invalid redirect address")`，
  否则等于用后一步的副产物冒充判据）。见 `tunnel/trojan/misdirection_probe_test.go` 与
  `tunnel/tls/fallback_probe_test.go`。
- **池化负载的归还点要用账目核，但"看着漏还"要先确认变量到底赋值没有**（2026-10-19）。
  `tproxy.packetDispatchLoop` 确实漏了两处：会话上限拒绝的 `continue`（**每丢一个包**永久离开池
  一块 8KB，而丢弃速率等于新源地址到达速率、源地址可伪造，AGENTS.md 早就把"会话上限拒绝"列进
  必须覆盖的丢包出口，dokodemo/socks 都做了，tproxy 是唯一违约方）和 `s.packetChan <- conn`
  撞 `ctx.Done` 的 `return`（手里那条 info 已离开 packetQueue，`Close` 的排空够不着它）。
  （同一份报告里那句「`s.packetChan <- conn` 撞 `ctx.Done` 时手里那条 info 得自己还」**后来被证伪**：负载在那之前已在写锁内投进 `conn.input`，`conn.Close()` 的排空就是归还点，再 `putPacketBuf` 一次是**双归还**——2026-10-27 由 HEAD 上的稳定红灯用例抓到并删掉。判据同时改成能区分「漏还」与「双归还」，因为它原先把后者报成前者。）
  但同一份审计报告里的第三处——队列读的 `case <-s.ctx.Done(): return`——**核实不成立**：那是
  `select { case info = <-s.packetQueue: … case <-ctx.Done(): return }`，走 ctx 分支时 `info`
  还没被赋值，队列里剩下的由 `Close` 的排空负责；在这里补一句 `putPacketBuf(info.payload)`
  不是修 bug，是造一个 nil 解引用或者双归还（后者比漏还严重：两个包共享同一块底层数组互相串数据）。
  判据 `tunnel/tproxy/pool_account_test.go` 两条新增用例：上限分支用"灌满会话表 + 投 N 个新源地址
  的包 + 拿已有会话的 input 当同步点"收成确定性的 N 块差额；关停分支把 `packetChan` 做成**无缓冲**
  让发送必然阻塞，轮询到 mapping 出现那条会话再 cancel，这样判的是发送分支而不是队列读那条与取包
  随机 select 的分支（拿后者当判据会偶发空转）。
- **关停时"已建会话但还没交给下游"的分支要自己删表项**（2026-10-20）。`dokodemo` 与 `tproxy` 的
  dispatch 在 `s.packetChan <- conn` 撞上 `ctx.Done` 时，负责 defer 删 mapping 的守护协程从没起来；
  只 `conn.Close()` 会让 mapping 永久挂着一条已 Close 的死会话（两条队列 + 会话 ctx），Close 之后
  仍被 Server 对象攥着。两处都在该分支的写锁内 `delete`。判据
  `tunnel/dokodemo/shutdown_mapping_test.go` 与 tproxy 扩展后的
  `TestPooledPayloadReturnedWhenDownstreamGone`：无缓冲 `packetChan` + 轮询到表项出现才 cancel；
  去掉 delete，两条都在"关停后 mapping 还剩 1 条"处红，且 8KB 负载仍随 `conn.Close()` 的排空归还。
  **判据别用包级借出计数的精确值跨用例量**：同包前一个用例的 dispatch/读泵若没等退出，会在下一条
  用例中途归还手里的 buffer，把计数从 base 压到 -1（`-race` 全仓跑实测过）。tproxy 三个 dispatch
  用例统一走 `startDispatch`/`stopDispatch`（cancel → 关 UDP 监听叫醒读泵 → `wg.Wait`），dokodemo
  那条则改看会话自己的 `input` 队列长度（同步点 1 → 关停后 0），不碰全局计数。
- **UDP dispatch 的读错误出口是退避重试，不是 return**（2026-10-20）。`dokodemo` 旧实现遇到任何
  `ReadFrom` 错误就返回，一次瞬时错误（部分平台会把 ICMP 相关错误冒到 `PacketConn` 上）就让 UDP
  转发永久停摆，直到重启进程；`socks`/`tproxy` 的同型循环早就是 100ms 退避 + 日志限流，现在三处
  一致。判据 `tunnel/dokodemo/read_error_test.go`（首次读失败、随后必须仍有包被投递出来；退回
  `return` 实测在 2s 处转红）。
- **`Proxy.Close` 必须排空转发缓冲池**（2026-10-27）。`boundedBufPool` 的两个池默认驻留
  8KB × 1024，`packetPool` 独立时再一份，合计最多约 16MB。进程退出时无所谓，但库化调用方
  （反复建栈 / 热重载）会一直攥着已关闭的 `Proxy`，这部分就成了常驻内存。`Close` 在
  `wg.Wait` 与隧道释放之后用非阻塞取空（**不关闭 channel**：关停超时留下的一两条中继协程
  仍可能 `Put`，关 channel 会 panic）。判据 `proxy/proxy_test.go` 的
  `TestCloseDrainsPooledRelayBuffers`，看的是池队列长度而不是"有没有复用"。
- **「拆会话」与「删表项」是两个必须被下游看见的事件**（2026-10-28，第 8 轮审计，5 处都有判据）：
  ① **`socks` 的 UDP 投递必须在 `mappingLock` 读锁内完成**（`deliverToSession`）。守护协程的收尾是
  「写锁 delete → `Close` 排空 input」，在锁外投递就可能在排空已跑完之后挤进一个包——每命中一次永久
  漏一块 8KB 池化负载，而触发它只需要客户端在拆会话的瞬间继续发包。tproxy（RLock 内投递）与
  dokodemo（同一临界区）早就这么做，socks 是三条姊妹入站里唯一违约的一条；判据
  `tunnel/socks/dispatch_teardown_race_test.go` 用包级同步点 `var dispatchGap`（形态同 `var dialUDP`，
  别当冗余删掉）把守护协程的收尾顺序逐字插进"已查到会话、尚未投递"的窗口，所以红绿与墙钟无关。
  ② **握手层 panic 之后要自己关连接**：`tunnel/websocket` 的 `AcceptConn` 新增 `connHandedOff` defer，
  panic（对端可控的升级请求）发生在 `trackPending` **之前**时连接既不在在途登记表、也没有活引用，
  fd 只能等运行时对 netFD 的 finalizer 回收；旧文档说的"handedOff 负责资源收尾"不成立，它只取消子
  ctx。所有把所有权交出去的出口（移交上游、交给 redirector 继续拷贝）必须先置位，判据
  `tunnel/websocket/handshake_panic_close_test.go`（含"重定向那条连接不许关"与"关停中被拒的握手必须关"
  两条对照；注入点是新增的 `var readUpgradeRequest`）。
  ③ **建栈失败路径上的句柄要跟着 success 标志兜住**：`tunnel/tls/server.go` 打开 keyLogger 之后还有
  `ValidateCurvePreferences` 这一道错误返回，客户端 `client.go` 早有 `keyLoggerOwned` defer，服务端这条
  姊妹路径漏了——实测 20 次失败留 20 个 fd（判据 `keylogger_fd_server_test.go`）。
  ④ **accept 循环的错误分支不许裸 `continue`**：`tunnel/adapter` 是全仓最后一个没有退避的入口，监听
  持续报错（fd 耗尽）就是满 CPU 的静默热循环；现在 100ms 退避 + 每 10 次一条 Error，判据
  `tunnel/adapter/accept_backoff_test.go` 跑在 Go 1.27 的 `testing/synctest` 气泡里（虚拟时间只在气泡内
  所有协程 durably blocked 时前进，所以"500ms 里退避几次"是精确读数；给 `failingListener` 设了
  `limit`/`onLimit` 这道自限位，否则退回裸 continue 时循环永不阻塞、判据只能报 test timed out 而不是原因）。
  ⑤ **限速排队必须听得到"这条会话正在被拆"**：`memory.waitPaced` 原先只绑 `u.ctx`（只有 `DelUser`/
  后端 `Close` 会取消它），于是被限速的 `Write` 可以按 n/limit 排队几十秒到几小时——relay 拆会话走的
  是关连接，`Proxy.Close` 的 5s 预算只是放弃等待、叫不醒它，期间那条协程攥着从 `boundedBufPool` 借走
  的中继 buffer（池被抽空后每包重新 make）。新增可选接口 `statistic.PacedTrafficMeter`
  （不改 `TrafficMeter`，不破坏任何既有实现），trojan 的 `InboundConn` 在 **Auth 成功之后**派生会话
  ctx、`Close` 里取消（失败路径不保证每个出口都走 Close，先建就是往 `s.ctx` 的 children 上挂没人收的
  一节）；`OutboundConn` 直接用已有的 `c.ctx`。合成两个唤醒源用 `pacingContext`：子 ctx 只活在这次
  等待里、`defer release()`，所以不累积。**等待被打断不等于流量没发生——账目照记**，两条判据都钉了
  这一点（`statistic/memory/limiter_stop_test.go` 用 synctest 量取消后的返回耗时，
  `tunnel/trojan/paced_session_ctx_test.go` 判真实接线；去掉接线实测 6.19s 变红）。
- **配了持久化后端就要让面板改库免重启生效**（2026-10-28，第 8 轮续）。SQLite 原先只在
  `NewAuthenticator` 装载时读一次 `expire`/`maxip`/限速，面板 `UPDATE` 完要重启才生效——按
  「接受但不生效算缺陷」那条口径处理。新增 `statistic/memory/refresh.go`：默认 60s（间隔存
  `sqliteRefreshInterval` 原子值，判据运行期调小），**一次 `ListUser` 全表读**（不是逐用户
  `LoadUser`，那等于每轮 N 次查询）把三列推进已存在的内存用户。三条边界：不新增也不删除用户
  （库里多出的行不该把 `DelUser` 掉的人捞活）；**绝不碰 `quota`/`quotaChecked`**（SQLite 表没有
  额度列，打开判定等于给所有 SQLite 用户设 `quota=0`，正是"拿零值当没有额度概念"那个坑）；
  走 `User` 上的内部 setter 而不是 `SetUserIPLimit`/`SetUserSpeedLimit`——后者会 `SaveUser`
  整行写回，读侧调它等于每轮重抄一遍并与 `batchTrafficUpdater` 抢流量列。**MySQL 必须关掉这条
  轮**（`memory.WithoutPersistencerRefresh(ctx)`）：`mysql.Authenticator` 内嵌同一个 memory 认证器、
  用的是同一份配置上下文，`mysql` 与 `memory.sqlite` 可以并存，两边推同一批用户就是互相盖值
  （SQLite 那条 `expire=0` 的镜像行会把 MySQL 里已到期的人复活成永不过期）。判据
  `statistic/memory/sqlite_refresh_test.go`：到期与续期**两个方向**都要翻得过来、抑制闸门确实
  没起轮、以及"跑出一段流量后仍不被当成额度为 0 拒掉"。
- **投递的结果必须决定日志措辞，且归还之后一个字都不许碰**（2026-10-28）。`tunnel/tproxy` 的
  found 分支原先两条出口共用一句 `tproxy packet sent`——队列满丢包时日志反而把排障方向带偏，
  而且 `len(info.payload)` 写在 `putPacketBuf` 之后，违反「Put 之后不许再碰那块 buffer」的跨层
  口径（今天只读切片头不会串数据，下一句改成读内容就是）。抽出 `deliverToSession(conn, info) bool`
  （false 即"已就地归还"），`size` 提到投递之前取。判据 `tunnel/tproxy/packet_deliver_test.go`：
  行为侧用池账目判"满队列时确实还了"（期望值是 `base + udpSessionQueueSize`，队列里那几块是
  合法借出的，别写成 `base`），接线侧照 `tunnel/http/h2_status_clamp_test.go` 的先例用读源码的
  结构性门禁——这条措辞分流做不出稳定的端到端形态（要捕获全局 logger 并与同包用例并发）。
- **给测试留的包级可调阈值必须用原子**（2026-10-27）。`simplesocks.headerTimeout` 原本是裸
  `time.Duration`，测试用 `defer` 恢复它，而 accept 已改为 worker 池：测试返回时某些 worker
  仍在 `readHeader` 里读它，`go test -race` 实测报 Write/Read 竞争（两条并发读头用例同时红）。
  现在存 `atomic.Int64`、0 表示取默认值，测试统一走 `setHeaderTimeoutForTest`（`t.Cleanup`
  恢复，排在 `server.Close` 之后）。新增"测试可调"的进程级阈值照这个形态写。

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