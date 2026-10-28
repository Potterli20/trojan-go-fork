<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/

Core abstraction layer. `Tunnel` is the sole plugin interface; 15 implementations live here.

## Interface contract (`tunnel.go`)

```go
type Tunnel interface {
    Name() string
    NewClient(ctx context.Context, client Client) (Client, error)
    NewServer(ctx context.Context, server Server) (Server, error)
}
```

- `Client` = `Dialer` + `io.Closer`; `Server` = `Listener` + `io.Closer`.
- Stack composition wraps: outer tunnel receives inner as `client`/`server` arg, delegates transport.
- A tunnel need not implement both roles — `NewClient`/`NewServer` may return `common.NewError("not supported")`. Examples: `tproxy` is server-only, `dokodemo` server-only.

## Metadata (`metadata.go`)

`Metadata{Command, Address}` + `Address{DomainName|IPv4|IPv6, Port, AddressType}`. Wire format: 1-byte cmd + 1-byte addrtype + addr + 2-byte port BE. `ReadFrom`/`WriteTo` on raw `io.Reader`/`Writer`. This is the **trojan wire header**; reused by `simplesocks` and `socks`.

## Registration

`RegisterTunnel(name, t)` called from each subdir's `init()`. `GetTunnel(name)` used by `proxy/stack.go`. No unregister. Re-registration panics.

## Subdirectories (15)

| Tunnel | Role | Notes |
|---|---|---|
| `trojan/` | proto | Main protocol. See `tunnel/trojan/AGENTS.md` |
| `tls/` | transport | TLS + uTLS fingerprint. See `tunnel/tls/AGENTS.md` |
| `websocket/` | transport | See `tunnel/websocket/AGENTS.md` |
| `mux/` | multiplex | smux wrapper. See `tunnel/mux/AGENTS.md` |
| `shadowsocks/` | crypto | See `tunnel/shadowsocks/AGENTS.md` |
| `simplesocks/` | proto | Trojan metadata without password (used inside mux) |
| `freedom/` | outbound | 直连出站；可选 forward-proxy（SOCKS5）与 `outbound_local_addr`/fwmark。历史上文档记的 “client.go:79 hardcoded localhost TODO” 已不存在，包内无任何 TODO |
| `transport/` | transport | TCP raw; fallback listener。关于 “导入 websocket/http 会成 import cycle” 的注释在 `server.go` 里（旧文档记的行号已漂移，按内容找） |
| `tproxy/` | inbound | **Linux-only** (`//go:build linux`). IP_TRANSPARENT |
| `socks/` | inbound | SOCKS5 server |
| `http/` | inbound | HTTP/HTTPS proxy server。h1 之外还嗅探 h2c 前文并整条交给 `h2.go`；h2 上的 CONNECT 显式回 501；没有任何目标主机时回 400（见下面 Conventions） |
| `adapter/` | inbound | Protocol sniffing dispatcher (socks vs http) |
| `router/` | routing | Geosite/geoip. See `tunnel/router/AGENTS.md` |
| `dokodemo/` | inbound | Fixed-target redirect (like v2ray dokodemo-door) |
| `quic/` | transport | QUIC 承载；服务端/客户端都显式开 RFC 9221 datagram。**标准 run-type 的栈里没有它，只有 `custom` 模式能到达**；trojan 的 UDP 走 stream 而非数据报 |

## Conventions specific to tunnels

- Config retrieved via `config.FromContext(ctx, Name).(*XxxConfig)` — never read JSON directly.
- Tunnel `Name()` MUST equal the config-registry name and the stack-list name. String identity matters.
- **没有** per-tunnel 实例 ID：`ctx.Value(Name+"_ID")` 永远取不到值（原先唯一的写入点已删除，且它的 key 类型是包内私有的、别人根本读不到）。需要按会话区分实例请另想机制。
- `Conn.Metadata()` returns negotiated `*Metadata` on inbound conns; outbound conns use `Metadata` passed to `DialConn`.
- **线上格式里的长度/偏移一律先升到 `int` 再参与加法。** `metadata.go` 里
  `make([]byte, length+2)`（`length` 是 `byte`）在 254/255 处回绕成 0/1，缓冲比域名还短，
  下一句 `buf[:length]` 直接切片越界 panic。影响面是全仓共用的握手解码：实测（HEAD 二进制）
  未认证的本地 SOCKS5 入口收到一个 255 字节主机名就整个进程崩掉——`socks` 这条链上没有
  recover（只有 `mux`/`tls`/`trojan` 三处有）。门禁见 `tunnel/metadata_wire_test.go`。
- **`http` 入站不许把"没有目标"退化成默认端口。** `resolveProxyTarget`（`server.go`）是
  h1 与 h2c 两条分支共用的唯一入口：`Host: example.com` 不带端口 → 按 80 转发（合法）；
  **完全没有** Host/`:authority` → 明确回 400。旧实现落到 `NewAddressFromHostPort("tcp","",80)`，
  目的地字符串是 `":80"`，而实测 `net.Dial("tcp", ":80")` **连的是 127.0.0.1** ——
  等于让代理入口替请求方访问代理自己的本地服务，且域名是空串时按域匹配的路由规则
  一条都不会命中（HEAD 上实测：一个不带 Host 的请求被正常转发、回 204）。
  判据在 `tunnel/http/target_required_test.go`，含"不带端口的合法 Host 仍须走 80"的对照。
- **relay 没有半关闭**：`proxy/proxy.go` 的 `relayConnLoop` 是「任一方向结束就拆掉整个会话」
  （两个 `io.CopyBuffer` 共享一个 `done`，谁先返回谁触发，随后 `defer` 关掉两端）。
  `tunnel.Conn` 接口也没有 `CloseWrite`，所以无法只断一个方向。写 tunnel 时必须假设：
  只要有一方读到 EOF/错误，另一方的数据就没了。典型受害者是「请求写完就 EOF」的形态——
  `tunnel/http` 因此把请求方向的 EOF 推迟到本请求会话真正结束（见 `server.go` 里
  `OtherConn.Close` 的注释）；任何新 tunnel 若自己会先产生 EOF，也要照这个思路处理。
- **UDP 会话 daemon 的 select 必须同时看 `s.ctx.Done()` 和 `conn.ctx.Done()`。** 入站 UDP
  按源地址（`ip:port`）在 `mapping` 里复用会话，daemon 负责在会话结束时删表项。relay 拆会话
  走的是 `conn.Close()`，它**只 cancel 会话自己的 ctx**（`s.ctx` 是 Server 级，要等整个进程
  关停）；daemon 少看 `conn.ctx.Done()` 就只能等空闲 timer 过期，最长一个 `UDPTimeout`。
  这期间表项是死的，而 dispatch 的 `found` 分支把后续包投进没人读的 `input` 就 `continue`
  ——**同一源端口重开会话等于黑洞**，不是"只是晚点恢复"。`socks` 从一开始就带着这条分支，
  是另外两条的参照实现；`dokodemo` 与 `tproxy` 是 2026-10-13 才补齐的，判据分别是
  `tunnel/dokodemo/session_close_test.go` 与 `tunnel/tproxy/session_close_test.go`（两者把
  `timeout` 故意设成 5 分钟，让"timer 早晚会清"这条自愈在水准线内不可用，从而测到的是窗口
  本身）。新增同类入站时把这条抄过去。
- **dispatch 的"查表 + 投递"必须在同一把 `mappingLock` 读锁里做完。** daemon 的收尾是
  「写锁 `delete` → `conn.Close()` 排空 `input`」，在锁外投递就可能落在"已 delete、排空也跑完"
  之后：那一包成功入队却再无读者，从池里借出的 8KB 负载**永久离开池**。后果要说准——它不是
  堆内存无限增长（会话对象连同那条包在 dispatch 迭代结束后就成垃圾被回收），而是**池化在这条
  路径上白做**：每命中一次多一次 8KB 分配与一轮 GC 压力，口径同 `tunnel/trojan/packet.go` 的
  池账目判据。客户端在拆会话的瞬间继续发包就能按包速率命中，源地址还能伪造。只有加锁是真互斥——
  在投递的 `select` 里加 `case <-conn.ctx.Done()` 挡不住，因为"表项还在、ctx 已取消"和
  "排空已跑完"是两个时刻。`tproxy`（RLock 内投递）与 `dokodemo`（查表与投递同一临界区）本来就对，
  `socks` 是三条姊妹入站里唯一违约的一条，2026-10-28 补成 `deliverToSession`；判据
  `tunnel/socks/dispatch_teardown_race_test.go` 用包级同步点 `var dispatchGap` 把 daemon 的收尾顺序
  插进窗口正中，所以红绿不吃墙钟。
- **`tproxy` 的 UDP 路径在没有 CAP_NET_ADMIN 时无法端到端测。** `ReadFromUDP` 依赖
  `IP_RECVORIGDSTADDR` cmsg，而它只在监听 socket 带 `IP_TRANSPARENT` 时由内核投递，普通 CI
  （ubuntu-24.04，非 root）构造不出真实入口。所以 `tproxyPacketInfo` 和 `packetQueue` 是
  `Server` 的字段而不是 `packetDispatchLoop` 的局部量——就为了测试能直接投伪造 info、跳开
  socket 层验证同一份会话代码。改成局部变量会把这条判据一起弄没。
- **`tunnel.Address` 的导出字段在构造/解析后视为不可变。** 一个 `*Address` 会被多条并发
  会话**共享**，这是既成事实不是疏忽：`dokodemo` 的 `s.targetAddr` 每实例一个，却塞进每条
  TCP 会话的 `targetMetadata` 和每个 UDP 会话的 `fixedMetadata`（`server.go:34`/`:135`），
  `freedom` 的 `c.proxyAddr` 同理，而 relay 给每条会话各起一个 goroutine。所以任何"顺手写回
  地址对象"的代码都是同一对象上的无锁并发读写。`ResolveIP` 过去把 DNS 结果写进导出的
  `net.IP` 就是这个形态——域名地址的序列化与 `String()` 只看 `DomainName`、根本不读 `IP`，
  写它既无读者又送一次竞争；现在缓存落在非导出的 `resolvedIP` 上、由 `resolveMu` 保护
  （缓存本身必须留着：`router` 对同一条连接会解析两次，去掉就是每次拨号多一轮 DNS）。
  判据见 `tunnel/metadata_shared_addr_test.go`，其中并发那条要在 `-race` 下跑。
- **已核实但故意没改的一处竞争**：`Metadata.WriteTo` 仍然写 `r.Address.NetworkType = "tcp"`。
  它在非测试代码里零读者（读者链只有 `Address.Network()` ← `Metadata.Network()`，后者没有
  调用点），却因上面那些共享对象被 `-race` 判死（16 条会话并发 `WriteTo` 即报）。删掉语义
  代价为零，但 `tunnel/metadata_appendto_test.go:123` 明确把这个副作用钉成"必须保住"，那是
  另一条工作线上的主动决定——**要动它先和那条线对齐**，不要单点修改把别人的门禁改红。

## Anti-patterns

- Don't call `net.Dial` directly in `NewClient` — always use the inner `client` arg. Breaking this breaks stack composition.
- Don't assume `client`/`server` arg is non-nil at the bottom of the stack — leaf tunnels (`freedom`, `transport`, `tproxy`, etc.) ignore it.
