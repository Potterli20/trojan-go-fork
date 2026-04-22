# 需求

有任何不明确和疑问都先询问,询问清楚后更正这里的需求内容,然后再进行开发.

## 完成情况
在以下表格中记录需求的完成情况
| 需求 | 状态 |
|---|---|
| 1. 指定 local IP (outbound_local_addr) | 已完成 |
| 2. 指定 fwmark (outbound_fwmark) | 已完成 |
| 3. gRPC API 动态配置 local IP / fwmark | 已完成 |

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