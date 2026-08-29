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