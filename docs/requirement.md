# 需求

有任何不明确和疑问都先询问,询问清楚后更正这里的需求内容,然后再进行开发.

## 完成情况
在以下表格中记录需求的完成情况
| 需求 | 状态 |
|---|---|
| 1. 指定 local IP (outbound_local_addr) | 已完成 |
| 2. 指定 fwmark (outbound_fwmark) | 已完成 |
| 3. gRPC API 动态配置 local IP / fwmark | 已完成 |
| 4. 修复多处 data race 风险 | 已完成（2026-08-29 回归审计） |
| 5. 修复服务端添加用户后 WebSocket 访问无效 | 已完成（2026-08-29 回归审计） |
| 6. 服务端支持使用 SQLite 实现用户数据持久化（仅 Linux） | 已完成（2026-08-29 回归审计） |
| 7. 支持指定转发 buffer 大小及数量限制 | 已完成（2026-08-29 补回数量限制） |
| 8. 修复服务端上行限速无效的问题 | 已完成（2026-08-29 修复方向映射回归） |
| 9. 修复连接转发阻塞导致 goroutine 泄露的问题 | 已完成（2026-08-29 修复 tls/socks channel 发送阻塞） |
| 10. 修复客户端 TCP 和 WebSocket 无法连接的问题 | 已完成（2026-08-29 修复 FindAllEndpoints 回归） |
| 11. 新增 TCP Fast Open 支持 | 已完成（2026-08-29 回归审计） |

## 4~11. 上游/社区修复项回归审计（2026-08-29）
README 中列出的 8 项社区改进（@fregie 等）在后续大规模重构中出现了不同程度的退化，
本轮逐项对照 fregie 原始实现（remote `fregie`，2021）做了回归审计与修复：

- **data race**：
  - `statistic/memory`：`MaxIPNum` 此前由 `setIPLimit` 无锁写入、`AddIP` 持 ipLock 读取，已统一用 ipLock 保护；
    `AddSentTraffic/AddRecvTraffic` 改为锁内取 limiter 指针、锁外 WaitN，避免限速阻塞期间 `SetSpeedLimit` 写锁饥饿。
  - 测试代码三处共享 `err` 变量的 race（transport/websocket/socks 测试）已修复。
  - 全量 `go test -race -tags full ./...` 通过。
- **WebSocket 添加用户**：共享认证器机制（`trojan.Auth` 包级变量 + `statistic.NewAuthenticator` 按 ctx 缓存）在位，
  TLS 子树与 WebSocket 子树的 trojan 服务端共用同一实例，API AddUser 对两条链路同时生效。api/service 测试通过。
- **SQLite 持久化**：`statistic/sqlite`（`linux && (amd64||386||arm||arm64)` 真实现，其余平台 no-op 桩）在位，
  memory 认证器经 `sqlite` 配置项启用，批量流量回写（batchTrafficUpdater）带指数退避重试。windows/darwin 交叉编译通过。
- **buffer 大小及数量限制**：大小限制（`relay_buffer_size`）在位；数量限制此前丢失，已补回：
  `relay_buffer_count`（默认 1024）+ `proxy.boundedBufPool`（channel 池，池满丢弃、池空临时分配），
  限制转发层常驻内存上限。
- **上行限速**：`tunnel/trojan/server.go` 的流量方向映射在 2026-04 被错误对调
  （Write→RecvLimiter、Read→SendLimiter），与 API 契约（Sent=DownloadTraffic、SendLimiter=下行、RecvLimiter=上行）相反，
  导致 API 的上行限速实际作用于下行。已恢复 fregie/上游语义：Write→AddSentTraffic，Read→AddRecvTraffic。
- **goroutine 泄露**：`tls/server.go` 与 `socks/server.go` 的 channel 发送无 ctx 保护，
  关闭时若下游停止消费且 channel 满，goroutine 永久阻塞、`Close()` 的 wg.Wait() 死锁。已补 select ctx.Done 分支。
- **客户端 TCP/WebSocket 无法连接**：`proxy/stack.go::FindAllEndpoints` 被改为 early-return，
  而服务端树构建对同一父节点两次 `BuildNext(trojan)`（复用同一节点并置 IsEndpoint），
  导致 trojan 节点既是端点又持有 mux→simplesocks 子树时，子树端点被整体丢弃——
  所有启用 mux 的服务端（TCP 与 WebSocket）连接永久滞留。已恢复 fregie 语义（收录自身并继续下钻）。
- **TCP Fast Open**：`common.Dial/common.Listen`（tfo-go，带 fallback）在位；
  服务端监听（transport）与客户端出站（freedom，`tcp.fast_open`，默认 true）均已接通。

### 2026-08-30 追加修复（第二轮回归排查）
- **log.ConnectionTracker data race**：`Success/Error`（连接建立方调用）与 `Destroy`（消费方关闭时调用）
  并发写 `endTime`/`fields`，已加锁保护。
- **select/default 判断关闭的陷阱**：`select { case <-ctx.Done(): default: log.Fatal() }` 中 default 恒就绪，
  当 ctx 恰已取消时两分支同时就绪被随机二选一，正常关闭路径约 50% 概率 os.Exit 杀死进程。
  tls/dokodemo/tproxy 共 4 处已改为直接检查 `ctx.Err()`，并把 accept 循环中的 Fatal 降级为 Error。
- **无 ctx 保护的 channel 发送**（关闭时消费端停止 → 发送方永久阻塞 → wg.Wait 死锁）：
  simplesocks（connChan/packetChan）、dokodemo（packetChan、input）、tproxy（packetChan、packetQueue、input）、
  http（CONNECT 分支 connChan）已补 select ctx.Done 分支或 default 丢包。
- **等待对端首字节的读无截止时间**（对端静默 → handler 永久阻塞 → Close 的 wg.Wait 挂起）：
  tls 握手+HTTP 嗅探（30s）、transport HTTP 嗅探（30s）、trojan Auth（10s）、adapter 协议嗅探（10s）
  已设截止时间，移交下游前解除。
- **recorder.Capacity 全局变量竞态**：改为 atomic，新增 SetCapacity。
- **common.PickPort 只探测 TCP**：adapter 等会在同一端口同时监听 TCP+UDP，
  现同时探测两种协议可用性，消除测试与服务启动的偶发 EADDRINUSE。

### 2026-08-30 追加修复（第三轮全量审计与现代化）
- **select/default 判断关闭残留 18 处**（同第二轮陷阱）：trojan/adapter/socks(x2)/http/mux/simplesocks
  的 accept 循环、redirector worker 关闭期不再阻塞在 copyWg.Wait()、proxy relay 双循环、
  quic accept、router packetLoop(x2)、http OtherConn.Read 关闭判定，全部改为直接检查 `ctx.Err()`。
- **库代码 panic 移除**：tproxy/socks PacketConn 的 ReadFrom/WriteTo（relay 只用 WithMetadata 方法，
  panic("implement me") 改为返回错误）；http OtherConn.Read 的 panic("non zero") 死分支改返回
  io.ErrUnexpectedEOF。
- **socks UDP 会话超时接线**：硬编码 5s 改为配置的 UDPTimeout（默认 60s，与 dokodemo/tproxy 对齐）。
  测试直接构造 Config 绕过 creator 默认值，UDPTimeout=0 会使会话立即超时——此前被 5s 掩盖，测试已显式设置。
- **等待对端首字节的读补齐截止时间**（第二轮遗漏的入站路径）：socks 握手（30s，connect 移交前解除、
  associate 用后即弃）、shadowsocks AEAD 探测读（10s，成功与重定向两路都解除）、http 首个请求与
  keep-alive 后续读（各 30s，读完即解除）、websocket 升级请求读（30s，超时走既有重定向清理）。
  实测：nc 静默连入 30s 被清退；SIGTERM 后 1s 内干净退出。
- **router init 中的 log.Fatal 移除**：GetAssetLocation 移入 config creator 闭包，失败回退裸文件名并
  Warn——加载期 geodata cache 会再次解析，届时失败仅跳过规则，包初始化阶段不应杀死进程。
- **dokodemo 持锁发送**：命中已有会话时持 mappingLock 阻塞发送 conn.input，缓冲满会卡死超时清理与
  Close 的锁竞争方；改为锁内只做查表/建表，解锁后 select+default 尽力发送。
- **trojan 包级 Auth 竞态**：check-then-set 无互斥，并发 NewServer 可能创建两个认证器互相覆盖；
  加包级 sync.Mutex。
- **memory 认证器关闭时序**：Authenticator 派生自有 ctx+cancel，batchTrafficUpdater 由裸 go 改
  wg.Go；Close 顺序改为先 cancel 停 updater、wg.Wait、最后关用户——若先关用户，ResetTraffic 清零后
  仍在跑的 updater 会读到 sent < persistedSent，uint64 下溢把天文数字写进 DB。
- **proxy 栈构建 Fatal 改错误传播**：BuildNext/LinkNextNode 返回 (*Node, error)，新增 BuildChain；
  配置错误等可恢复场景不再 os.Exit，部分失败 cancel 已构建部分。LinkNextNode 失败回滚挂链。
- **mux stickyConn AEAD 竞态**（-race 发现）：同一 stickyConn 被并发 DialConn 失败清理/cleanLoop/
  Client.Close 并发 Close，padding 写与数据写并发进入无锁的 trojan/shadowsocks AEAD 层破坏 nonce；
  加 writeMu + closeOnce，SetWriteDeadline 纳入锁内。
- **Go 1.27 现代化**：User 的 Sent/Recv/lastSent/lastRecv 改 atomic.Uint64；剩余 10 处
  wg.Add(1)+go func 改 wg.Go；go fix（1.27 规则）检查无命中。
- **基础设施**：Makefile 清理未定义的 $(GO_DIR)、新增 test-race 目标；AGENTS.md 的 CI 章节修正为
  实际存在的 workflow；component/AGENTS.md 修正 mini 标签描述（仅排除 api，仍含 forward/nat/mysql）。

### 2026-08-30 追加修复（第四轮全量审计与现代化）
- **mysql 认证器生命周期重写**（与第三轮 memory 修复同类问题的残留）：
  updater 由裸 go 改 wg.Go；派生自有 ctx + cancel，原先监听外部 ctx 导致 Close 后 updater 仍
  在操作已关闭的 memory 认证器；新增 Close()（先停 updater → wg.Wait → 关 memory → 关 db，
  顺序同 memory 的时序约束）；check_rate 非正数时兜底 30s（time.NewTicker 对非正周期 panic）。
- **mysql syncUsers 数据破坏与连接泄漏**：db.Query 的 rows 全路径从不 Close（每轮泄漏一条连接）；
  Scan 失败 break 后继续用不完整的 userMap 清理，把合法用户整体误删；rows.Err() 从未检查。
  现查询失败/迭代失败直接放弃本轮同步（只记日志，不做清理），rows 显式 Close。
- **trojan client 协议静默损坏**：DialConn 的后台 goroutine 100ms 先写协议头失败时 sync.Once
  已消耗且返回 (false, nil)，后续 Write(p) 走裸写分支把不带协议头的 payload 直接发出，
  服务端认证必然失败且无任何报错。改为 headerMu + atomic headerWritten + headerFailed：
  失败后连接标记不可用，后续写入显式报错；并发时等待写头完成而非竞速。
- **tproxy TCP REDIRECT 模式失效**（2022-02 pr @3F2CF810 引入的回归）：getOriginalTCPDest 被
  简化为直接返回 LocalAddr，丢弃了 SO_ORIGINAL_DST 查询——iptables REDIRECT/DNAT 下拿到的是
  改写后的本机地址，流量被转发到错误目的地。已恢复 getsockopt 实现（v4/v6 双分支），
  查询失败（TPROXY 模式无 conntrack DNAT 记录）回退 LocalAddr，两种模式均兼容；
  staticcheck 报的 getsockopt U1000 即此 bug 的信号。
- **资源泄漏修复（8 处）**：router DialPacket 下游失败不关 directConn；freedom DialPacket
  forwardProxy 分支各失败路径不关 packetConn / socks TCP 连接；simplesocks DialPacket 写
  associate 失败不关 conn；tproxy UDP 回包 Write 失败不关透明 socket；quic Listen 失败不关
  packetConn；dokodemo/adapter NewServer UDP 失败不关 tcpListener；tproxy DialUDP FileConn
  失败路径 fd double-close（fd 号被复用会误关无关 fd）。
- **无超时拨号**：tls client 直连分支与 redirector defaultDial 的裸 net.Dial 加 30s 超时
  （无超时拨号会被不可达目标拖住约 2 分钟，后者还阻塞 Redirector.Close 的 wg.Wait）。
- **proxy relay 元数据防御**：inbound.Metadata() 为 nil（栈配置不当）时不再 nil-deref。
- **废弃 API 与死代码清理**：grpc.Dial/WithInsecure → grpc.NewClient/insecure.NewCredentials；
  删除 Go 1.18 起被忽略的 PreferServerCipherSuites（连 config schema 的 prefer_server_cipher/
  curves 死键与示例配置一并清理，这两个键从未实际生效）；删除从未实现的内部占位字段
  （tls curve/portOverrider/reuseSession、api control key/cert/list、api service client 4 字段、
  custom convert 死函数）；mysql 错误文案小写化。
- **保留项（有意）**：x509.DecryptPEMBlock（key_password 传统加密私钥支持，标准库无替代）；
  config context key 用字符串（公开惯例，AGENTS.md 已记载）；dokodemo PacketConn.src 为
  staticcheck 误报（server.go 回包/超时清理实际使用）。
- **验证**：go vet、staticcheck（仅剩保留项）、全量测试 28 包、-race 全量、9 种 build tag 组合、
  windows/darwin/386/arm64 交叉编译全部通过。

### 2026-08-30 追加修复（第五轮全量审计与现代化）
- **websocket 迁移回归排查（无发现）**：逐行核对 coder/websocket v1.8.15 的 Accept/Dial/NetConn 源码——
  fakeHTTPResponseWriter 的 Hijack/WriteHeader 协议与库的 `hijacker()` + `errCallerOwnerConn` 机制吻合；
  客户端经固定 DialContext 的 http.Transport 完成 RFC 6455 升级后连接所有权移交正确；
  NetConn 后重设读上限、InboundConn.Close 调用 cancel、写帧后 Flush 均确认无误。
- **tproxy IPv6 UDP 全链路修复**（三个叠加缺陷）：
  1. cmsg 过滤用 v4 的 `IP_RECVORIGDSTADDR`(20) 同时匹配两层，而 IPv6 内核投递
     `(SOL_IPV6, IPV6_RECVORIGDSTADDR=74)`，永远不命中 → 任意一个 IPv6 UDP 包即
     "unable to obtain original destination" → 读循环 `s.Close()` 杀死整个 tproxy 服务
     （远程 DoS）。现在按 Level 分流匹配，ListenUDP 对 IPv6 socket 补设
     `SOL_IPV6/74` 选项（stdlib 部分架构缺该常量，自定义 `ipv6RecvOrigDstAddr=0x4a`；
     按 LocalAddr 实际 family 判断，避免对绑定 v4 地址的 AF_INET socket 误设报错）。
  2. AF_INET6 分支对 16 字节 `RawSockaddrInet4` 做 28 字节强转越界读（读出垃圾
     当 v6 地址，流量被转发到错误目的地）；改为直接 unsafe cast `msg.Data`
     （即 native 布局的 sockaddr_in/in6），并加长度校验。
  3. `udpAddrToSocketAddr` 对 Zone="" 的全球单播 `ParseUint` 必败 → 回包 DialUDP 失败；
     `udpAddrFamily` 第二个条件误写 `laddr.IP.To4()` 两次（应为 raddr），混合族场景
     EAFNOSUPPORT。均已修。
- **tproxy UDP 读循环防误杀**：单次读失败即 `s.Close()` 杀整个服务（TCP+UDP 同死）；
  改为连续 10 次失败（约 1s）才判定 socket 永久损坏，单次失败记日志后重试。
- **tproxy/dokodemo UDP 会话中毒修复**：会话 goroutine 因 DialUDP/Write 失败退出时
  只 cancel 不删 mapping（tproxy），dokodemo 更是连 conn.Close 都没有（relay goroutine
  永久阻塞在 output 发送）。两个会话 goroutine 统一改为 defer 删 mapping + 关会话，
  任意失败路径后同源新包都能建立新会话。
- **trojan server 重定向路径 RewindConn 无界缓冲**（fork commit 5bc1d696 引入的回归）：
  Auth 失败分支只 `Rewind()` 不 `StopBuffering()`，buffering 期间底层读到的所有字节
  继续 append 进 buf（超过 bufferSize*2 只打日志不停累积），重定向中继的整个生命周期
  按流量速率无上限吃内存，可被低带宽打 OOM。对齐 shadowsocks/websocket 同路径的
  先 Rewind 后 StopBuffering 语义。
- **trojan Auth 56 字节 hash 改 io.ReadFull**：单次 Read 不保证读满，TCP 分段时合法
  客户端被误判重定向（函数内其余字段均已用 io.ReadFull）；连接已有 10s 截止时间，
  无阻塞风险。
- **router packetLoop 无限重试**：底层会话被 trojan 层关闭后返回非 EOF 包装错误
  （common 错误无 Unwrap，errors.Is 穿透不了），循环以 10Hz 空转且消费端永久阻塞；
  两个读循环加连续失败计数（10 次约 1s），超阈值 cancel 自身 ctx 让消费端随
  ctx.Done 返回 EOF 干净退出，单次错误仍可重试。
- **api/service 测试弃用 API 清理**（第四轮只修了非测试代码的残留）：
  `grpc.Dial/WithInsecure` → `grpc.NewClient/insecure.NewCredentials`。
- **go fix（Go 1.27 规则）**：简化 websocket server 的嵌套结构体字面量。
- **保留项（有意）**：memory_test 的空分支为有意注释占位、未用测试辅助函数
  （AGENTS.md 不清理预先存在的代码）；memory.go 重试循环与 trojan client 延迟写头的
  `time.After` 为正确用法（非热循环，timer 自然到期释放）。
- **验证**：go build/vet（full 标签）、staticcheck（仅剩既知保留项）、-race 全量 28 包、
  windows/amd64、darwin/arm64、linux/386、linux/arm64 交叉编译通过。

## 1. 指定local IP
需要能在配置文件中指定代理使用的local ip,只需要指定由本服务和需要代理的目标之间所建立的tcp或udp所使用的本地ip是什么,主要用于能够使用策略路由控制实际的出口接口.

### 规格(澄清后)
- **作用范围**: 仅作用于 freedom 出站 tunnel。覆盖:
  - 服务端连接真实目标的 TCP/UDP
  - 客户端 router bypass 直连(通过 freedom)
  - transport client 底层直连(通过 freedom)
- **配置位置**: `tunnel/freedom` 的 Config,字段名 `outbound_local_addr` (YAML: `outbound-local-addr`)
- **格式**: 仅 IP 字符串(IPv4 或 IPv6),如 `"192.168.1.100"` 或 `"::1"`。源端口固定为 0(系统分配)
- **TCP/UDP 统一**: 同一个 `outbound_local_addr` 同时作用于 TCP 的 `net.Dialer.LocalAddr` 和 UDP 的 `net.ListenPacket` 绑定地址
- **未配置时**: 保持现有行为(系统自动选择源 IP)
- **解析失败时**: `NewClient` 返回错误,启动中止

## 2. 指定 fwmark (仅 Linux)
除通过源 IP 做策略路由外,还需支持通过给出站 socket 打 fwmark (SO_MARK) 来触发 `ip rule fwmark X lookup Y` 类型的策略路由。

### 规格(澄清后)
- **作用范围**: 与 outbound_local_addr 一致,仅 freedom 出站 TCP 和 UDP socket 调用 `setsockopt(SOL_SOCKET, SO_MARK, mark)`
- **配置位置**: `tunnel/freedom` 的 Config,字段名 `outbound_fwmark` (YAML: `outbound-fwmark`),类型 int
- **取值**: 0 或未配置 = 不打 mark;非 0 值 = 设置 SO_MARK 为该值
- **平台**:
  - Linux 下生效
  - 非 Linux 平台(darwin/windows/freebsd 等)配置了非 0 值仅打印 warning 日志并忽略,不影响启动
- **权限**: 设置 SO_MARK 需要 CAP_NET_ADMIN(容器通常已 privileged),失败时 dial 报错并返回
- **非 Linux 编译**: 用 build tag 拆分,无 syscall 污染

## 3. 通过GRPC API配置local IP 和 fwmark
增加接口来配置这两项,如果配置文件中已经配置了,则覆盖原有配置

### 规格(澄清后)
- **实现方式**: 在 `tunnel/freedom` 包中维护全局共享状态 (local IP + fwmark),由 `sync.RWMutex` 保护。`freedom.Client.DialConn/DialPacket` 在每次拨号时读取该全局状态。启动时 `NewClient` 把配置文件的值写入全局状态(仍保留原 Client 字段作为 fallback/初始化值)
- **API 新增 RPC** (仅 TrojanServerService,`-tags api` 下):
  - `SetOutboundConfig(SetOutboundConfigRequest) → SetOutboundConfigResponse`
  - `GetOutboundConfig(GetOutboundConfigRequest) → GetOutboundConfigResponse`
- **请求字段**:
  - `local_addr` (string): IP 字符串,空串 = 清除(回到系统默认源 IP)
  - `fwmark` (int32): 非 0 = 设置;0 = 清除
- **语义**:
  - API 调用立即覆盖配置文件的值,对后续新建的出站连接生效(已存在的连接不受影响)
  - 不持久化到配置文件,进程重启后回到配置文件的值
  - Linux 以外平台 fwmark 设置成功但不生效(保持警告语义,与需求 2 一致)
- **错误处理**:
  - `local_addr` 非法 IP → 返回 success=false 附带 info
  - 其他情况 → success=true