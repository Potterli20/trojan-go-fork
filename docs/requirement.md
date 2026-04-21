# 需求

有任何不明确和疑问都先询问,询问清楚后更正这里的需求内容,然后再进行开发.

## 完成情况
在以下表格中记录需求的完成情况
| 需求 | 状态 |
|---|---|
| 1. 指定 local IP (outbound_local_addr) | 已完成 |

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
