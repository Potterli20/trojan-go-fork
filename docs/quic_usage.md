# QUIC/HTTP3 使用指南

## 概述

trojan-go-fork 实现了 QUIC 隧道（`tunnel/quic`，基于 `quic-go v0.62.1`），把 trojan 栈跑在 UDP 上。

**可达性（先读这条）**：QUIC 已在 `component/client.go` 和 `component/server.go` 中导入，因此**已注册**——
但注册 ≠ 进栈。`client` / `server` / `forward` / `nat` 四种运行类型的栈是硬编码的，里面没有 `QUIC` 这一层
（`proxy/client/client.go:GenerateClientTree`、`proxy/server/server.go`），所以**只写一个顶层 `quic:` 段
不会启用 QUIC**，必须用 `custom` 运行类型把 `quic` 作为节点写进 `path`。下面两个坑是实跑验证时踩到的：

1. 每个节点的 `config` 会被解码进**所有**已注册模块，而节点只能看到自己的 ctx。于是 quic 节点需要的证书
   必须写在 **quic 节点自己的 `config` 里**（`ssl: {cert, key}`），写在下游 `tls` 节点里它看不见，
   报错形态是 `QUIC requires TLS certificate and key`。
2. 服务端 quic 节点的 `remote-addr` / `remote-port` 语义是**监听地址**（源码直接
   `net.ListenUDP(cfg.RemoteHost, cfg.RemotePort)`），反直觉但如此。

## 配置示例

### 客户端（`custom`）

```yaml
run-type: custom

inbound:
  node:
    - protocol: adapter
      tag: adapter
      config:
        local-addr: 127.0.0.1
        local-port: 17000
    - protocol: socks
      tag: socks
      config:
        local-addr: 127.0.0.1
        local-port: 17000
  path:
    - - adapter
      - socks

outbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: 127.0.0.1        # 对端地址
        remote-port: 17443
        quic:
          alpn: hq-29                 # ALPN 标识
          max-idle-timeout: 30        # 空闲超时（秒）
          max-incoming-streams: 100   # 最大并发流
          initial-stream-window: 0    # 0 = 交给 quic-go 库内默认（512KB）
          initial-conn-window: 0      # 同上
          insecure: false             # 是否跳过证书验证；自签测试证书需改成 true（见下）
          congestion: bbr             # bbr / brutal / force-brutal / reno
          brutal-up: 0                # 单位 bps，0 = 不生效
          brutal-down: 0              # 单位 bps
        ssl:
          sni: localhost
          verify: false
    - protocol: tls
      tag: tls
      config:
        ssl:
          sni: localhost
          verify: false
    - protocol: trojan
      tag: trojan
      config:
        password:
          - "your-password"
  path:
    - - quic
      - tls
      - trojan
```

### 服务端（`custom`）

```yaml
run-type: custom

inbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: 127.0.0.1        # 这里语义是监听地址
        remote-port: 17443
        quic:
          alpn: hq-29
          max-idle-timeout: 30
          max-incoming-streams: 100
          congestion: bbr
        ssl:                          # 证书必须写在 quic 节点自己的块里
          cert: server.crt
          key: server.key
          verify-hostname: false
          cert-check-rate: 1
    - protocol: tls
      tag: tls
      config:
        ssl:
          cert: server.crt
          key: server.key
          verify-hostname: false
          cert-check-rate: 1
    - protocol: trojan
      tag: trojan
      config:
        remote-addr: 127.0.0.1        # 诱饵/回落地址
        remote-port: 17099
        password:
          - "your-password"
  path:
    - - quic
      - tls
      - trojan

outbound:
  node:
    - protocol: freedom
      tag: freedom
      config: {}
  path:
    - - freedom
```

> 服务端 trojan 节点在构造时会**实拨一次诱饵地址**：不配 `remote-addr/port` 报
> `invalid redirect address. check your http server: :0`，配了但对端没监听报 `connection refused`。
> 所以诱饵服务必须先于代理启动。

### 关于证书校验（实测）

QUIC 传输层自己会做一次 TLS 校验，`ssl.verify: false` **管不到它**——那个键作用在内层 tls 节点上。
控制 QUIC 层的是 `quic.insecure`。两种失败形态（都跑过）：

- 自签证书 + `insecure: false` → `CRYPTO_ERROR ... x509: certificate signed by unknown authority`
- 证书已过期 + `insecure: false` → `x509: certificate has expired or is not yet valid: ...`

`insecure: true` 会跳过 QUIC 层这两类校验（拿一张已过期的证书配 `insecure: true` 实测仍能建连，
三次请求全 200）——所以它只适合本地验证，生产环境请用 CA 证书并保持 `insecure: false`。

## 特性说明

### 1. 拥塞控制算法

`congestion` 的取值来自 `tunnel/quic/congestion.go:ApplyCongestionControl` 的 switch，只有这四个：

- **bbr**: xray-core 的 BBR（`profile = "standard"`），也是留空时的回退值
- **brutal**: 固定速率发送（xray-core brutal），**要求 `brutal-up` 和 `brutal-down` 都 > 0**，实际速率取二者较小值
- **force-brutal**: 同上，但**只需要 `brutal-up` > 0**
- **reno**: 不改拥塞算法，沿用 quic-go 给这条连接装的默认值

其它取值会记一条 `Unknown congestion control: <x>, falling back to BBR` 的告警并回退 BBR。
**`cubic` 不在支持列表里**——写 `congestion: cubic` 拿到的其实是 BBR。

```yaml
quic:
  congestion: bbr
```

### 2. Brutal 限速

Brutal 是固定速率发送（不探测带宽），适用于需要稳定码率的场景。它是 `congestion` 的一个**取值**，
不是叠加在别的算法上的开关：

```yaml
quic:
  congestion: brutal     # 或 force-brutal
  brutal-up: 10000000    # 单位是 bps，这里是 10 Mbps
  brutal-down: 50000000  # 单位是 bps，这里是 50 Mbps
```

**注意**：`brutal_up` / `brutal_down` 的单位是 **bps**（`NewBrutalSender(bps uint64, ...)`），
按 Mbps 填会把速率压小 10^6 倍。`congestion: bbr` 时这两个键不会被读取。

### 3. ALPN

ALPN 用来让两端确认"这条 QUIC 连接上跑的是本协议"：

- 默认 `hq-29`，客户端与服务端必须一致，否则握手失败
- 它只是一个 ALPN 字符串。这条隧道承载的是 trojan 字节流，**并没有实现 HTTP/3 语义**，
  所以不要用外部 HTTP/3 客户端来连它
- 留空会回落到 `hq-29`（`client.go` / `server.go` 里都有 `if cfg.QUIC.ALPN == ""`）

### 4. 性能优化参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `max_idle_timeout` | 30 | 连接空闲超时时间（秒）；保活周期取它的一半 |
| `max_incoming_streams` | 100 | 允许的最大并发流数 |
| `initial_stream_window` | 0 | 0 = 交给 quic-go 库内默认（512KB） |
| `initial_conn_window` | 0 | 同上 |

调优建议：
- **高带宽低延迟网络**：显式增大窗口（如 262144 = 256KB）。注意**别照抄旧文档的 65535**——库内默认是 512KB，填 65535 等于把窗口缩到 1/8。
- **移动网络**：保持默认（0），交给库决定
- **多用户场景**：增加 `max_incoming_streams`

## 使用场景

### 1. 绕过基于 TCP 的 QoS

QUIC 走 UDP，可避开针对 TCP 的 QoS 整形。注意测试流量必须**真的经过代理**才有意义——
直接 `curl https://...` 是绕开代理裸连的，量不出隧道行为。按上面客户端示例（SOCKS 监听
`127.0.0.1:17000`）：

```bash
# 启动客户端
./trojan-go-fork -config quic_client.yaml

# 走 SOCKS5 代理测速
curl --socks5-hostname 127.0.0.1:17000 -o /dev/null -w "%{time_total}\n" https://example.com/
```

### 2. 弱网环境优化

在高延迟、高丢包环境下，QUIC 相比 TCP 有明显优势：

```yaml
# 优化配置（写在 quic 节点的 config 块里）
quic:
  max-idle-timeout: 60           # 延长超时（保活周期随之变成 30s）
  initial-stream-window: 131072  # 显式增大窗口
  initial-conn-window: 131072
  congestion: bbr                # BBR 自带探测，弱网下一般优于固定码率的 brutal
```

### 3. 多路复用

QUIC 在一条连接上跑多条流，单条流的丢包阻塞不会波及别的流。并发验证：

```bash
# 并发请求测试（同样要显式走代理）
for i in {1..10}; do
  curl --socks5-hostname 127.0.0.1:17000 -o /dev/null -w "%{time_total}\n" https://example.com/file$i &
done
wait
```

> 这条隧道本身还套着 trojan；`mux` 是另一层（TCP 侧的多路复用），二者不需要同时开。

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

先确认实际生效的拥塞算法。把 `log-level` 设为 `0`（本仓库级别是反的，`0` 最啰嗦），会看到：

```text
[QUIC] [conn=xxxx] New connection accepted from 127.0.0.1:xxxx, congestion=bbr, alpn=[hq-29]   # 服务端
[QUIC] [conn=xxxx] Dialing to 127.0.0.1:17443 with congestion=bbr, alpn=[hq-29]                  # 客户端
[ApplyCongestionControl] [ENTRY] ... Algorithm="brutal", BrutalUp=10000000 bps, BrutalDown=50000000 bps
```

`Algorithm=` 就是配置里读到的值；若它落在 `Unknown congestion control` 分支，日志里会同时出现
`falling back to BBR`——说明你写的算法名不在 `bbr` / `brutal` / `force-brutal` / `reno` 之内。

尝试切换算法：
```yaml
quic:
  congestion: reno  # 从 bbr 换成 quic-go 的默认拥塞控制
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

- **2026-10-13**: 与代码对齐重写。删掉不存在的 `quic.enabled`（2026-10-11 已移除）；
  示例从 `run-type: client/server` 改为 `custom`（内置栈里没有 QUIC 层，旧示例照抄不会启用 QUIC）；
  两个初始窗口默认值 65535 → 0；`brutal` 单位 Mbps → bps；拥塞算法列表 `bbr/cubic` →
  `bbr/brutal/force-brutal/reno`（`cubic` 会回退 BBR）；补上节点级 `ssl` 与诱饵地址两条实跑坑，
  以及自签/过期证书在 `insecure` 开关下的实测表现
- **2026-09-19**: 启用 QUIC 隧道支持，对齐 quic-go v0.62.1 API
- **2026-09-19**: 修复 QUIC 读 goroutine 泄漏问题
- **2026-09-19**: 改用具体类型 (`*quic.Conn`) 替代 `any` 断言
