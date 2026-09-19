# QUIC/HTTP3 使用指南

## 概述

trojan-go-fork 已启用 QUIC 隧道支持（基于 `quic-go v0.62.1`），提供 HTTP/3 协议能力。

**注意**：QUIC 已在 `component/client.go` 和 `component/server.go` 中导入，可在配置中使用。

## 配置示例

### 客户端配置

```yaml
run-type: client
local-addr: 127.0.0.1
local-port: 10808
remote-addr: example.com
remote-port: 443

# QUIC 配置
quic:
  enabled: true                    # 启用 QUIC
  max-idle-timeout: 30            # 最大空闲超时（秒）
  max-incoming-streams: 100       # 最大入站流数
  initial-stream-window: 65535    # 初始流窗口大小
  initial-conn-window: 65535      # 初始连接窗口大小
  alpn: hq-29                     # ALPN 标识
  insecure: false                 # 是否跳过证书验证
  congestion: bbr                 # 拥塞控制算法 (bbr/cubic)
  brutal-up: 0                    # Brutal 上行限速 (Mbps, 0=禁用)
  brutal-down: 0                  # Brutul 下行限速 (Mbps, 0=禁用)

ssl:
  verify-hostname: false
  key: /path/to/key.pem
  cert: /path/to/cert.pem
  sni: example.com
```

### 服务端配置

```yaml
run-type: server
local-addr: 0.0.0.0
local-port: 443
password:
  - your-password

# QUIC 配置（与服务端 TLS 配置分离）
quic:
  enabled: true
  max-idle-timeout: 30
  max-incoming-streams: 100
  initial-stream-window: 65535
  initial-conn-window: 65535
  alpn: hq-29
  congestion: bbr
  brutal-up: 0
  brutal-down: 0

ssl:
  verify-hostname: false
  key: /path/to/server.key
  cert: /path/to/server.crt
  sni: example.com
```

## 特性说明

### 1. 拥塞控制算法

支持以下拥塞控制算法：

- **bbr**: Google BBR 拥塞控制（推荐）
- **cubic**: Linux 默认 CUBIC 算法

配置方式：
```yaml
quic:
  congestion: bbr
```

### 2. Brutal 加速

Brutal 是一种基于延迟反馈的自适应限速算法，适用于高延迟网络。

```yaml
quic:
  brutal-up: 10     # 上行 10 Mbps
  brutal-down: 50   # 下行 50 Mbps
```

**注意**：Brutal 仅在 `congestion: cubic` 时有效，BBR 自带速率控制。

### 3. ALPN 协商

ALPN (Application-Layer Protocol Negotiation) 用于协商应用层协议：

- `hq-29`: HTTP/3 draft-29（当前默认）
- 其他版本可根据需要修改

### 4. 性能优化参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `max_idle_timeout` | 30 | 连接空闲超时时间（秒） |
| `max_incoming_streams` | 100 | 允许的最大并发流数 |
| `initial_stream_window` | 65535 | 初始流窗口大小（字节） |
| `initial_conn_window` | 65535 | 初始连接窗口大小（字节） |

调优建议：
- **高带宽低延迟网络**：增大窗口大小（如 256KB）
- **移动网络**：保持默认或减小窗口
- **多用户场景**：增加 `max_incoming_streams`

## 使用场景

### 1. 绕过 QoS 限制

QUIC 基于 UDP，可绕过基于 TCP 的 QoS 限制：

```bash
# 启动客户端
./trojan-go -config quic_client.yaml

# 测试速度
curl -o /dev/null -w "%{time_total}\n" https://www.google.com
```

### 2. 弱网环境优化

在高延迟、高丢包环境下，QUIC 相比 TCP 有明显优势：

```yaml
# 优化配置
quic:
  enabled: true
  max-idle-timeout: 60        # 延长超时
  initial-stream-window: 131072  # 增大窗口
  initial-conn-window: 131072
  congestion: bbr            # BBR 更适合弱网
```

### 3. 多路复用

QUIC 原生支持多路复用，避免队头阻塞：

```yaml
# 并发请求测试
for i in {1..10}; do
  curl -o /dev/null -w "%{time_total}\n" https://example.com/file$i
done
```

## 故障排查

### 1. 连接失败

检查日志：
```bash
log-level: 0  # 显示所有日志
```

常见问题：
- **证书验证失败**：设置 `insecure: true`（仅测试环境）
- **端口被占用**：确认 UDP 443 未被其他服务占用
- **防火墙拦截**：确保 UDP 443 端口开放

### 2. 性能不佳

检查拥塞控制：
```go
// 日志中会显示实际使用的拥塞算法
log.Debug("congestion=", s.congestion)
```

尝试切换算法：
```yaml
quic:
  congestion: cubic  # 从 bbr 切换到 cubic
```

### 3. 连接不稳定

调整超时参数：
```yaml
quic:
  max-idle-timeout: 120      # 延长空闲超时
  max-incoming-streams: 200  # 增加并发流
```

## 测试验证

运行单元测试：
```bash
SHADOWSOCKS_SF_CAPACITY="-1" go test -v -count=1 -race -tags full ./tunnel/quic/...
```

运行集成测试：
```bash
SHADOWSOCKS_SF_CAPACITY="-1" go test -v -count=1 -race -tags full ./test/scenario/...
```

## 注意事项

1. **UDP 依赖**：QUIC 基于 UDP，某些网络环境可能限制 UDP 流量
2. **NAT 兼容性**：大多数现代 NAT 设备支持 UDP，但老旧设备可能有兼容性问题
3. **TLS 握手**：QUIC 将 TLS 1.3 整合到握手过程，首包即可加密传输
4. **迁移成本**：从 TCP 迁移到 QUIC 需要客户端和服务端同时支持

## 参考资源

- [quic-go 官方文档](https://github.com/quic-go/quic-go)
- [HTTP/3 RFC 9114](https://datatracker.ietf.org/doc/rfc9114/)
- [QUIC 拥塞控制最佳实践](https://quic.edm.uh.edu/)

## 更新日志

- **2026-09-19**: 启用 QUIC 隧道支持，对齐 quic-go v0.62.1 API
- **2026-09-19**: 修复 QUIC 读 goroutine 泄漏问题
- **2026-09-19**: 改用具体类型 (`*quic.Conn`) 替代 `any` 断言
