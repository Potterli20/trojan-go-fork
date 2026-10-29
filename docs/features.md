# 特性详解

Trojan-Go Fork 兼容原版 Trojan 协议及配置文件格式。使用以下扩展特性时，请确保通信双方均使用 Trojan-Go Fork。

从 README 的[特性一览](../README.md#特性一览)拆出来：那里只有概览表，这里写用法、配置键与已知坑。

- [可移植性](#可移植性) · [简易配置](#简易配置) · [WebSocket](#websocket) · [HTTP/2 代理入口（h2c）](#http2-代理入口h2c)
- [重定向（诱饵）与客户端地址注入](#重定向诱饵与客户端地址注入) · [HTTP/3 QUIC 隧道](#http3-quic-隧道) · [多路复用](#多路复用) · [路由模块](#路由模块)
- [AEAD 加密](#aead-加密) · [传输层插件](#传输层插件) · [管理接口与流量后端](#管理接口与流量后端)

## 可移植性

编译得到的单个可执行文件，不依赖其他组件。支持交叉编译，可方便地部署到服务器、PC、树莓派甚至路由器上。

例如，交叉编译一个适用于 mips 处理器、Linux 系统、仅包含客户端功能的版本：

```shell
CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -tags "client" -trimpath -ldflags "-s -w -buildid="
```

## 简易配置

配置文件兼容原版 Trojan 格式，未指定的字段将使用默认值。

**服务端配置 `server.json`**：

```json
{
  "run_type": "server",
  "local_addr": "0.0.0.0",
  "local_port": 443,
  "remote_addr": "127.0.0.1",
  "remote_port": 80,
  "password": ["your_awesome_password"],
  "ssl": {
    "cert": "your_cert.crt",
    "key": "your_key.key",
    "sni": "www.your-awesome-domain-name.com"
  }
}
```

**客户端配置 `client.json`**：

```json
{
  "run_type": "client",
  "local_addr": "127.0.0.1",
  "local_port": 1080,
  "remote_addr": "www.your-awesome-domain-name.com",
  "remote_port": 443,
  "password": ["your_awesome_password"]
}
```

同样支持 YAML 格式。以下为等价的客户端配置 `client.yaml`：

```yaml
run-type: client
local-addr: 127.0.0.1
local-port: 1080
remote-addr: www.your-awesome-domain-name.com
remote-port: 443
password:
  - your_awesome_password
```

> 完整配置选项和示例请参考 `example/` 目录下的 `client.json`、`server.json`、`client.yaml`、`server.yaml`。

## WebSocket

Trojan-Go Fork 支持 TLS + WebSocket 承载 Trojan 协议，可利用 CDN 进行流量中转。

在服务端和客户端配置中同时添加 `websocket` 选项即可启用：

```json
{
    "websocket": {
        "enabled": true,
        "path": "/your-websocket-path",
        "host": "www.your-awesome-domain-name.com"
    }
}
```

`host` 可以省略，但服务端和客户端的 `path` 必须一致。开启 WebSocket 后，服务端可同时兼容
WebSocket 和标准 Trojan 流量。两侧的 `host` 含义并不相同：客户端这一侧它被写进握手的
`Host` 头（CDN 按这个头回源），服务端这一侧它**不参与任何校验**，只在 `remote-addr` 留空时
被拿去当诱饵地址兜底。完整链路和 Nginx/CDN 回源写法见 [CDN 中转配置](config-examples.md#cdn-中转配置)。

> 键名是 `host`，**不是 `hostname`**：`tunnel/websocket/config.go` 没声明 `hostname`，
> 写错不报错、只是被静默忽略（Go 的 json 行为）。

> 注意：标准 Trojan 不支持 WebSocket。如需使用 WebSocket 承载流量，请确保通信双方均使用 Trojan-Go Fork。

## HTTP/2 代理入口（h2c）

`tunnel/http`（本地 HTTP 代理入口）会嗅探 HTTP/2 前文：一条连接只要以 `PRI *` 开头，
就整条交给 h2c（先前知识、明文）处理。**绝对 URI 形态**的代理请求 —— 即浏览器对明文
代理开启 `proxy-http2` 后发出的那一种 —— 每条流拆成一个独立的转发会话，上层
（trojan / mux / simplesocks…）看到的形状与 HTTP/1.1 完全一致。

不需要改配置，也不新增任何配置项：嗅探是自动的，h1 连接照旧走 h1，同一条连接不会
混用两种版本。

能核对的边界：

- **CONNECT 不支持，显式回 501。** Go 的 http2 服务端没有实现 `http.Hijacker`
  （整个包里零命中），桥不出裸隧道。要 CONNECT 请用 HTTP/1.1。
- **这不是「HTTP/2 承载」。** h2c 只存在于本地代理入口这一跳；trojan / mux / quic
  等承载层里没有任何 h2 帧处理。`ssl` 段的 `alpn` 仍然不要设成 `["h2"]` —— 远端
  协商到 h2 只会把字节送进一个没人解析的分支。
- `tunnel/http` 依旧没有注册 config creator，所以配置里写 `"http2": { ... }`
  不会被解析。这不是漏了实现，而是这个模块本来就没有可配项。

承载层的队头阻塞仍然要靠这两节解决（h2c 只解决本地代理入口那一跳的并发）：

- [多路复用](#多路复用)：`mux`，在一条 TCP/TLS 连接上跑多个流；
- [HTTP/3 QUIC 隧道](#http3-quic-隧道)：QUIC 原生多流。`quic` 配置已注册、字段也确实被读取
  （不像上面那个 `http2` 段），但要注意它只在 `custom` 运行类型下进栈。

> 将来若要给**承载层**加 HTTP/2，得先给那个模块注册 config creator，否则新配置项
> 依旧是一句空话 —— 这正是本节曾经出错的地方。

## 重定向（诱饵）与客户端地址注入

认证失败或载荷不合法时，连接会被交给 `redirector` 原样中继到一个回退地址，让探测者
以为撞上了真站点。回退地址由 `ssl.fallback_addr` / `ssl.fallback_port`（TLS 层）或
服务端的 `remote-addr` / `remote-port`（trojan 层）决定。

默认**一个字节都不改**。要让回退服务器看到客户端地址，显式打开顶层开关
（YAML `redirect-client-ip`，JSON `redirect_client_ip`）：

```yaml
redirect-client-ip: true
```

打开后，重定向流量里的 **HTTP 请求**会被追加两行请求头再转发；已有
`X-Forwarded-For` 时是追加而不是覆盖：

```
X-Forwarded-For: <客户端地址>
X-Real-IP: <客户端地址>
```

三条边界都是有意为之：

- **取的是 TCP 对端地址。** trojan 自己的 `trust-headers` 会读
  `CF-Connecting-IP` / `X-Forwarded-For`，但走到重定向这一支时认证已经失败、那个 IP
  还没解析出来。节点挂在 CDN 后面时，这里就是 CDN 的地址。
- **不是 HTTP 请求的载荷原样中继。** 裸 TCP 探测、其他协议的握手，即使里面带
  `\r\n\r\n` 也不会被加头 —— 加了就把那个协议自己的字节改坏了。旧实现会加头，
  并且在"读不出完整头部块"时直接掐断连接（等于向探测者自曝），现在改为继续中继。
- **开关默认关。** 打开意味着把客户端地址交给回退服务器那一方，属于对外发送数据，
  由运维显式决定。

## HTTP/3 QUIC 隧道

Trojan-Go Fork 实现了 QUIC 隧道（`tunnel/quic`，基于 quic-go），用 UDP 承载替代 TCP+TLS。

**可达性**：QUIC 目前只在 `custom` 运行类型下可用。`client` / `server` / `forward` / `nat`
的栈是硬编码的，里面没有 `QUIC` 这一层（`proxy/client/client.go:GenerateClientTree`、
`proxy/server/server.go`），所以只写一个 `"quic": {...}` 段**不会**让隧道生效——必须把
`quic` 作为节点放进 `inbound` / `outbound` 的 `path`。

```yaml
run-type: custom
outbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: 127.0.0.1
        remote-port: 17443
        quic:
          alpn: hq-29
          max-idle-timeout: 30
          congestion: bbr
        ssl:            # 证书/SNI 必须写在 quic 节点自己的块里，写在下游 tls 节点看不到
          sni: localhost
          verify: false
    - protocol: trojan
      tag: trojan
      config:
        password: ["your_password"]
  path:
    - - quic
      - tls
      - trojan
```

服务端把 `remote-addr` / `remote-port` 当作**监听地址**（反直觉，但源码如此：
`net.ListenUDP(cfg.RemoteHost, cfg.RemotePort)`），并把 `ssl` 换成带 `cert` / `key` 的一组。

QUIC 传输层自己会校验证书，`ssl.verify: false` 管不到它——要跳过就得设 `quic.insecure: true`
（仅限本地自签测试；生产用 CA 证书并保持 `insecure: false`）。

> **两层校验是独立的**（实测）：`custom` 栈里 `quic` 节点和它下层的 `tls` 节点各自验一次证书。
> 用自签证书本地跑通，需要**同时**放开 `quic.insecure: true` 与两处 `ssl.verify: false`——
> 只放开 QUIC 那一层，内层 TLS 仍会报 `x509: certificate signed by unknown authority`。

`quic` 段字段（见 `tunnel/quic/config.go`）：

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `max_idle_timeout` | int | 30 | 空闲超时（秒）；**客户端**保活周期取其一半（`tunnel/quic/client.go` 设 KeepAlivePeriod=idle/2，服务端的 quic.Config 没有这一项） |
| `max_incoming_streams` | int | 100 | 最大并发流数 |
| `initial_stream_window` | int | 0 | 0 = 交给 quic-go 库内默认（单流 2MB、连接 3MB） |
| `initial_conn_window` | int | 0 | 同上 |
| `alpn` | string | `hq-29` | ALPN 标识，留空回落到 `hq-29` |
| `insecure` | bool | false | 跳过证书校验（仅测试环境） |
| `congestion` | string | `bbr` | `bbr` / `brutal` / `force-brutal` / `reno`，其它值告警并回退 BBR |
| `brutal_up` / `brutal_down` | uint64 | 0 | 单位 **bps**。`brutal` 需要两者都给（取较小值），`force-brutal` 只需 `brutal_up` |

> **`quic.enabled` 这个键不存在**（2026-10-11 删除：它带 `false` 默认值却全仓无人读，会让人误以为隧道默认关闭）。

特性边界：QUIC DATAGRAM 扩展已启用，但 trojan 的 UDP 走专用 stream，因此数据报路径只有
在 `custom` 里直接配对 PacketConn 时才会用到；**0-RTT 连接恢复未启用**（代码里没有 `Allow0RTT`）。
QUIC 基于 UDP，某些网络环境可能限制 UDP 流量。

**数据报路径的三条既有约束**（2026-10-15 核实并修复，判据见 `tunnel/quic/leak_fix_test.go`）：

1. **死连接会被作废**。客户端只缓存一条 `*quic.Conn`，过去唯一的失效点在 `DialConn` 的
   开流失败分支上——对端重启 / NAT 重绑 / 空闲超时之后，`DialPacket` 仍把那条死连接交出去，
   每路 UDP 秒断且永不重连（TCP 方向反倒会自愈）。现在读路径拿到连接级错误就作废缓存，
   下一拨重建。自己这侧的 ctx 取消不算（那是上游拆会话，连接还活着）。
2. **关掉一路 UDP 会话不会拆掉整条连接**。服务端的接收泵现在能在会话关闭时退出（此前它会
   带着 16 个数据报缓冲长期占住一个 goroutine，同连接后续 UDP 全丢）。注意 `packetDone`
   只在**连接级**结束才点亮：会话级关闭也点它的话，`handleConnection` 会 return 并把整条
   连接 `CloseWithError`，殃及同连接上还在跑的 stream。
3. **数据报是整包语义**。调用方缓冲装不下时报错拆会话，不再 `copy` 截断把半包当完整包交出去。
   另外 QUIC 数据报**不带 in-band 目标地址**（trojan/simple-socks 的 datagram 里有），
   所以读出来的 `Metadata.Address` 是 nil；`proxy/proxy.go` 的 `relayPacketLoop` 在写出前
   统一拦空地址，把原本会在 sink 里空指针崩进程的情况降级为单条会话退出。
   这里刻意不用 `conn.RemoteAddr()` 顶替——那会把代理服务器自己的地址当成数据报来源交给应用。

详细配置指南请参考 [QUIC 使用文档](docs/quic_usage.md)。

## 多路复用

Trojan-Go Fork 支持基于 [smux](https://github.com/xtaci/smux) 的多路复用，通过单条 TLS 连接承载多条 TCP 连接，减少 TLS 握手延迟，提升高并发场景下的性能。

> 启用多路复用不会提高单链路的测速，但能显著降低大量并发请求时的延迟，例如浏览包含大量图片的网页。

客户端启用多路复用：

```json
{
    "mux": {
        "enabled": true
    }
}
```

只需在客户端启用即可，服务端会自动检测并适配。

## 路由模块

内置路由模块，支持自定义分流策略。客户端与服务端均可使用。支持三种策略：

| 策略 | 说明 |
|------|------|
| `proxy` | 代理：通过隧道交给下一层处理（客户端为远程服务端；服务端为出站栈） |
| `bypass` | 绕过：本地直接连接目标 |
| `block` | 封锁：直接关闭连接 |

客户端配置示例：

```json
{
    "router": {
        "enabled": true,
        "bypass": [
            "geoip:cn",
            "geoip:private",
            "full:localhost"
        ],
        "block": [
            "cidr:192.168.1.1/24"
        ],
        "proxy": [
            "domain:google.com"
        ],
        "default_policy": "proxy"
    }
}
```

### 服务端分流（forward_proxy 配合路由）

服务端同样支持路由模块。当服务端配置了 `forward_proxy`（出站上游 SOCKS5 代理）时，
可以配合路由实现"只允许部分目标地址经过上游转发，其余直连"的分流效果：

- 命中 `proxy` 规则的目标：经 `forward_proxy` 上游转发；
- 命中 `bypass` 规则（含 `default_policy`）的目标：由本机直连目标，绕过上游；
- 命中 `block` 规则的目标：直接拒绝。

服务端配置示例（只允许访问本地回环段的目标走上游转发，其余直连）：

```json
{
    "forward_proxy": {
        "enabled": true,
        "proxy_addr": "127.0.0.1",
        "proxy_port": 1080
    },
    "router": {
        "enabled": true,
        "domain_strategy": "as_is",
        "default_policy": "bypass",
        "proxy": [
            "cidr:127.0.0.1/32"
        ]
    }
}
```

> 服务端未启用路由时，所有出站流量统一走 `forward_proxy`（若启用）。

## AEAD 加密

支持基于 Shadowsocks AEAD 对 Trojan 协议流量进行二次加密，确保 WebSocket 传输流量不被不可信的 CDN 识别：

```json
{
    "shadowsocks": {
        "enabled": true,
        "password": "my-password"
    }
}
```

> 服务端和客户端必须同时开启并使用相同的密码。

## 传输层插件

支持可插拔的传输层，兼容 Shadowsocks [SIP003](https://shadowsocks.org/en/wiki/Plugin.html) 标准的混淆插件。以下为使用 `v2ray-plugin` 的示例：

> **注意**：此配置仅作演示用途，不保证安全性。

**服务端**：

```json
{
    "transport_plugin": {
        "enabled": true,
        "type": "shadowsocks",
        "command": "./v2ray-plugin",
        "arg": ["-server", "-host", "www.baidu.com"]
    }
}
```

**客户端**：

```json
{
    "transport_plugin": {
        "enabled": true,
        "type": "shadowsocks",
        "command": "./v2ray-plugin",
        "arg": ["-host", "www.baidu.com"]
    }
}
```

---

## 管理接口与流量后端

服务端用户默认只存在内存里（顶层 `password` 数组），进程重启即丢流量计数。两种落盘方式：

- **SQLite**：顶层给一枚 `sqlite` 路径即可，没有独立配置节。仅 linux amd64/386/arm/arm64
  有实现，其它平台在启动期直接失败而不是静默降级。不需要额外 build tag。表的 `expire` 列
  （Unix 秒，0 = 永不过期）由 AutoMigrate 自动补上，装载时判定，之后认证器每 **60 秒**从库里
  重新采纳 `expire` / `maxip` / 上下行限速——面板直接 `UPDATE` 这几列**不用重启进程**就生效。
  额度（`quota`）不是 SQLite 的概念，那条只有 MySQL 有。
- **MySQL**：`mysql.enabled: true` 后 trojan 入站改用 MySQL 认证器，按 `check_rate` 秒与
  `users` 表双向同步。启动时会**幂等建表并给旧表自动补列**（缺 `maxip` / `expire` 都能补上），
  表结构为 `username,password,quota,download,upload,maxip,expire` 七列：
  `password` 列存的是密码的 SHA224 十六进制串，`expire` 是 Unix 秒级到期时间戳（0 = 永不过期）。
  `full` / `mini` 构建本来就带这个后端。

gRPC 管理接口用于运行时查用户、看流量记录、增删改用户，以及读写出站配置：

```json
{
    "api": {
        "enabled": true,
        "api_addr": "127.0.0.1",
        "api_port": 10000
    }
}
```

配套需要 `api` 构建标签（`full` 已含，**`mini` 不含**）。用内置命令行客户端访问：

```bash
./build/trojan-go-fork -api list -api-addr 127.0.0.1:10000
./build/trojan-go-fork -api record -api-addr 127.0.0.1:10000
./build/trojan-go-fork -api set --add-profile -target-password new_password_here -api-addr 127.0.0.1:10000
```

三条必须知道的边界（2026-10-13 实测）：

1. **`api_addr` / `api_port` 没有默认值，省略不等于不监听**。实测只写
   `"api": {"enabled": true}` 会绑到 `*:40127`、`*:40439`——所有网卡 + 内核随机端口，而且不止一个。
   **这个形态现在直接拒绝启动**：非回环监听必须至少有一层凭据（`api.token`，或
   `api.ssl.enabled` + `verify_client`），否则建栈失败、进程非零退出；`api.allow_insecure: true`
   是显式逃生阀。本机管理就把 `api_addr` 固定写成 `127.0.0.1`，跨机管理配 token 并自己加上
   TLS 与防火墙。配了 token 之后命令行要带 `-api-token`。
2. **API 起不来是静默的**：它的返回值在 goroutine 里被丢弃，端口冲突时代理照常运行、日志里一句
   话都不说。确认在不在只看这行 `server-side api service is listening on <addr>`。
   （上面那条暴露面闸门是例外：它在建栈阶段同步执行，所以是非零退出码而不是静默。）
3. **`api.ssl.enabled: true` 之后内置 `-api` 连不上**：命令行客户端固定用明文传输
   （`insecure.NewCredentials()`），实测报 `error reading server preface: EOF`。加密与内置工具二选一；
   `-api-token` 也是明文 metadata，所以"TLS + token"这个组合内置命令行给不了。

字段表、`api.ssl.verify_client` / `client_cert` 的 mTLS 用法、以及 `-api` 全部参数见
[配置说明](docs/configuration.md)的「管理接口 (`api`)」与「用户与流量后端」两节。
