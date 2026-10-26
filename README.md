# Trojan-Go Fork

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.27-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)

> **注意**：本项目已通过 AI 辅助修复和修改，当前服务端功能正常，客户端可能存在部分问题。欢迎提交 PR 协助修复。

Trojan-Go Fork 是基于 [p4gefau1t/trojan-go](https://github.com/p4gefau1t/trojan-go) 的社区维护分支。由于原项目已停止维护，本 Fork 持续修复 bug、合并社区贡献并改进功能。

---

## 目录

- [与原版的差异](#与原版的差异)
- [Docker 部署](#docker-部署)
- [快速开始](#快速开始)
  - [简易模式（命令行参数）](#1-简易模式命令行参数)
  - [配置文件模式](#2-配置文件模式)
  - [URL 模式（客户端）](#3-url-模式客户端)
- [特性介绍](#特性介绍)
  - [可移植性](#可移植性)
  - [简易配置](#简易配置)
  - [WebSocket](#websocket)
  - [HTTP/2 代理入口（h2c）](#http2-代理入口h2c)
  - [重定向（诱饵）与客户端地址注入](#重定向诱饵与客户端地址注入)
  - [HTTP/3 QUIC 隧道](#http3-quic-隧道)
  - [多路复用](#多路复用)
  - [路由模块](#路由模块)
  - [AEAD 加密](#aead-加密)
  - [传输层插件](#传输层插件)
  - [管理接口与流量后端](#管理接口与流量后端)
- [配置示例](#配置示例)
  - [Cloudflare HTTP/3 配置](#cloudflare-http3-配置)
  - [CDN 中转配置](#cdn-中转配置)
  - [IPv6 专用配置](#ipv6-专用配置)
- [构建指南](#构建指南)
- [故障排查](#故障排查)
- [图形界面客户端](#图形界面客户端)
- [致谢](#致谢)

---

## 与原版的差异

本 Fork 在原版基础上进行了以下改进和修复：

- [x] 修复多处 data race 风险
- [x] 修复服务端添加用户后 WebSocket 访问无效的问题
- [x] 服务端支持使用 SQLite 实现用户数据持久化（仅 Linux）
- [x] 支持指定转发 buffer 大小及数量限制，更好地控制内存占用
- [x] 修复服务端上行限速无效的问题
- [x] 修复连接转发阻塞导致 goroutine 泄露的问题
- [x] 修复客户端 TCP 和 WebSocket 无法连接的问题
- [x] 新增 TCP Fast Open 支持
- [x] 加固 sniff/rewind 缓冲，增加绝对上限，防止探测阶段缓冲区无界增长
- [x] 修复路由模块正则规则运行时并发写缓存导致的 fatal error（改为启动期预编译）
- [x] 补全 SQLite / MySQL 统计后端的连接与句柄关闭，MySQL 使用带上下文的查询并设置连接池上限
- [x] TLS 服务端关闭时排空待处理连接，QUIC 层改用 quic-go 具体类型消除 `any` 断言
- [x] 新增顶层优雅关闭：`Proxy.Run` 监听 SIGINT/SIGTERM，收到信号后取消上下文并释放资源。关闭的每个环节（等中继 goroutine 退出、等隧道关闭）各受 5 秒超时兜底，超时或收到二次信号都立即跳过等待继续释放，底层卡死也不会让进程退不出去；关闭错误会经 `RunAndClose` 冒泡到退出码，同一 `transport.Server` 被多个端点共用时 `Close` 幂等只跑一次

合并了以下社区贡献者的改进：[@fregie](https://github.com/fregie/trojan-go)、[@rezaf28](https://github.com/rezaf28)、[@lakwsh](https://github.com/lakwsh/trojan-go)、[@lbsystem](https://github.com/lbsystem)。

如发现其他 bug 或新需求，欢迎提交 [Issue](https://github.com/Potterli20/trojan-go-fork/issues)。

---

## Docker 部署

预构建的 Docker 镜像可从以下仓库获取：

- **Docker Hub**：[trliwork/trojan-go-fork](https://hub.docker.com/r/trliwork/trojan-go-fork/tags)
- **GitHub Container Registry**：[ghcr.io/potterli20/trojan-go-fork](https://github.com/Potterli20/trojan-go-fork/pkgs/container/trojan-go-fork)

拉取镜像：

```shell
# Docker Hub
docker pull trliwork/trojan-go-fork:latest

# GitHub Container Registry
docker pull ghcr.io/potterli20/trojan-go-fork:latest

# Podman
podman pull trliwork/trojan-go-fork:latest
```

运行容器：

```shell
docker run \
    --name trojan-go-fork \
    -d \
    -v /etc/trojan-go-fork/:/etc/trojan-go-fork \
    --network host \
    ghcr.io/potterli20/trojan-go-fork:latest
```

或指定自定义配置文件路径：

```shell
docker run \
    --name trojan-go-fork \
    -d \
    -v /path/to/host/config:/path/in/container \
    --network host \
    ghcr.io/potterli20/trojan-go-fork:latest \
    /path/in/container/config.json
```

---

## 快速开始

预编译二进制文件可在 [Release 页面](https://github.com/Potterli20/trojan-go-fork/releases) 下载，解压后直接运行，无其他依赖。

### 1. 简易模式（命令行参数）

**服务端**：

```shell
sudo ./trojan-go-fork -server -remote 127.0.0.1:80 -local 0.0.0.0:443 \
    -key ./your_key.key -cert ./your_cert.crt -password your_password
```

**客户端**：

```shell
./trojan-go-fork -client -remote example.com:443 -local 127.0.0.1:1080 -password your_password
```

### 2. 配置文件模式

```shell
./trojan-go-fork -config config.json
```

JSON / YAML 皆可，逐字段说明见 [配置说明](docs/configuration.md)。

### 3. URL 模式（客户端）

```shell
./trojan-go-fork -url 'trojan-go://password@example.com/?type=ws&path=%2Fpath&host=your-site.com'
```

只接受 `trojan-go://` scheme——传 `trojan://` 会在启动期报 `url does not have a trojan-go:// scheme`
（实测）。`type=ws` 时 `path` 必填且必须以 `/` 开头。

配套开关 `-url-option` 只认三个键：`mux` / `listen` / `api`，默认值是
`mux=true;listen=127.0.0.1:1080`——**不写 `-url-option` 也会开多路复用**，想关掉要显式传：

```shell
./trojan-go-fork -url 'trojan-go://password@example.com/' -url-option 'mux=false;listen=127.0.0.1:1080'
```

`mux` 的值不是合法 bool 时报 `invalid mux value: <值>`；键值对之间用 `;` 分隔，少写 `=`
报 `invalid option format: <pair>. expected key=value`（两条错误文案都实测过）。

---

## 特性介绍

Trojan-Go Fork 兼容原版 Trojan 协议及配置文件格式。使用以下扩展特性时，请确保通信双方均使用 Trojan-Go Fork。

### 可移植性

编译得到的单个可执行文件，不依赖其他组件。支持交叉编译，可方便地部署到服务器、PC、树莓派甚至路由器上。

例如，交叉编译一个适用于 mips 处理器、Linux 系统、仅包含客户端功能的版本：

```shell
CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -tags "client" -trimpath -ldflags "-s -w -buildid="
```

### 简易配置

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

### WebSocket

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
被拿去当诱饵地址兜底。完整链路和 Nginx/CDN 回源写法见 [CDN 中转配置](#cdn-中转配置)。

> 键名是 `host`，**不是 `hostname`**：`tunnel/websocket/config.go` 没声明 `hostname`，
> 写错不报错、只是被静默忽略（Go 的 json 行为）。

> 注意：标准 Trojan 不支持 WebSocket。如需使用 WebSocket 承载流量，请确保通信双方均使用 Trojan-Go Fork。

### HTTP/2 代理入口（h2c）

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

### 重定向（诱饵）与客户端地址注入

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

### HTTP/3 QUIC 隧道

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

### 多路复用

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

### 路由模块

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

#### 服务端分流（forward_proxy 配合路由）

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

### AEAD 加密

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

### 传输层插件

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

### 管理接口与流量后端

服务端用户默认只存在内存里（顶层 `password` 数组），进程重启即丢流量计数。两种落盘方式：

- **SQLite**：顶层给一枚 `sqlite` 路径即可，没有独立配置节。仅 linux amd64/386/arm/arm64
  有实现，其它平台在启动期直接失败而不是静默降级。不需要额外 build tag。
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

---

## 配置示例

### Cloudflare HTTP/3 配置

> **先读这条**：下面两份配置是 `custom` 模式，不是 `client` / `server`。QUIC 只在 `custom` 下可达
> （内置运行类型的栈里没有 QUIC 层），`quic.enabled` 这个键也不存在。能在这里核实的是
> trojan-go ↔ trojan-go 的 QUIC 隧道；至于公网 QUIC 能否抵达你的服务端，取决于 UDP 直通 /
> Spectrum 的 UDP 转发，那不属于本仓库的代码。

**客户端配置 (`cloudflare_quic_client.yaml`)**：

```yaml
run-type: custom

inbound:
  node:
    - protocol: adapter
      tag: adapter
      config: {local-addr: 127.0.0.1, local-port: 10808}
    - protocol: socks
      tag: socks
      config: {local-addr: 127.0.0.1, local-port: 10808}
  path: [[adapter, socks]]

outbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: your-domain.com
        remote-port: 443
        quic:
          alpn: hq-29
          max-idle-timeout: 30
          max-incoming-streams: 100
          initial-stream-window: 0     # 0 = quic-go 库内默认（单流 2MB、连接 3MB）
          initial-conn-window: 0
          congestion: bbr
          insecure: false              # 自签测试证书需改成 true
        ssl:
          sni: your-domain.com
          verify: true
    - protocol: tls
      tag: tls
      config:
        ssl: {sni: your-domain.com, verify: true}
    - protocol: trojan
      tag: trojan
      config:
        password: [your-strong-password]
  path: [[quic, tls, trojan]]
```

**服务端配置 (`cloudflare_quic_server.yaml`)**：

```yaml
run-type: custom

inbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: 0.0.0.0           # 这里语义是监听地址
        remote-port: 443
        quic:
          alpn: hq-29
          max-idle-timeout: 30
          max-incoming-streams: 100
          congestion: bbr
        ssl:                           # 证书必须写在 quic 节点里
          cert: /path/to/cert.pem
          key: /path/to/key.pem
          cert-check-rate: 1
    - protocol: tls
      tag: tls
      config:
        ssl:
          cert: /path/to/cert.pem
          key: /path/to/key.pem
          cert-check-rate: 1
    - protocol: trojan
      tag: trojan
      config:
        remote-addr: 127.0.0.1         # 诱饵/回落地址，需先于本进程启动
        remote-port: 80
        password: [your-strong-password]
  path: [[quic, tls, trojan]]

outbound:
  node:
    - protocol: freedom
      tag: freedom
      config: {}
  path: [[freedom]]
```

完整可拷贝的版本在 `config/cloudflare_quic_client.yaml` 和 `config/cloudflare_quic_server.yaml`；
TCP 降级（真的用 `run-type: client`）见 `config/cloudflare_tcp_fallback.yaml`。

### CDN 中转配置

用 WebSocket 把流量藏在 CDN 后面，隐藏源站真实 IP。

先记住栈的顺序，很多"照着配就是连不上"都栽在这里：服务端是
`transport → tls → websocket → shadowsocks → trojan`（`proxy/server/server.go`），
**只有启用 `transport-plugin` 才会跳过 `tls` 那一层**。所以 CDN 回源到源站这一跳
要么继续说 TLS，要么只做 L4 直通，不能在 nginx 上终结 TLS 之后用明文 `http://` 回源——
那一层的对端是个 TLS 端口，明文进去握手必挂。

同理，`local-port` 不能再写 443：nginx 已经在监听 443，同机再绑一次就是
`address already in use`。让 trojan-go 只监听回环上的 8443，公网侧只暴露 nginx。

**服务端配置**：

```yaml
run-type: server
local-addr: 127.0.0.1     # 只监听回环：公网必须从 CDN 进来
local-port: 8443
remote-addr: 127.0.0.1    # 诱饵地址：路径不匹配、也不是 WS 升级的请求都转去这里，
remote-port: 8080         # 这个端口上必须真有个站点在听，否则探测者直接吃 connection refused
password:
    - your_password
ssl:
    cert: /path/to/fullchain.pem
    key: /path/to/privkey.pem
    sni: your-domain.com
websocket:
    enabled: true
    path: /trojan-ws      # 只认这一条路径；服务端没有"校验 Host"这回事
shadowsocks:
    enabled: true
    method: AES-128-GCM
    password: ws-password
```

`websocket.host` 在服务端**不是**要匹配的 Host 头：它只在 `remote-addr` 留空时被拿去
当诱饵地址兜底（`tunnel/websocket/server.go`）。Host/Origin 对不上是 CDN 回源的常态，
一律放行，真正的鉴权在 trojan 层的密码上。要写诱饵就直接写 `remote-addr`/`remote-port`。

**Nginx 回源（二选一）**：

形态 A —— L4 直通，TLS 仍然由 trojan-go 终结。改动最小，也最省 CPU，
Cloudflare 这类只允许 HTTPS 回 443 的 CDN 用这种：

```nginx
stream {
    server {
        listen 443;
        proxy_pass 127.0.0.1:8443;
        proxy_timeout 3600s;   # 空闲隧道不要被 10 分钟默认值掐掉
    }
}
```

形态 B —— nginx 终结 TLS 并转发 HTTP，只在需要和静态站共用一个 location 时用。
回源这一跳仍然必须是 `https://`，并且长连接要放宽读超时，否则空闲会话会被 60s 默认值杀掉：

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;

    ssl_certificate     /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location /trojan-ws {
        proxy_pass https://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_ssl_server_name on;      # 回源也要带上 SNI
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    location / {                     # 其余路径就是普通站点，兼作探测者的诱饵
        root /var/www/html;
    }
}
```

**客户端配置**：`remote-addr` 写 CDN 域名而不是源站 IP，WS 的 path/host 跟 CDN 上的
location 对齐，两边 `shadowsocks` 的 method/password 必须成对一致。

```yaml
run-type: client
local-addr: 127.0.0.1
local-port: 1080
remote-addr: your-domain.com   # CDN 边缘，不是源站
remote-port: 443
password:
    - your_password
ssl:
    sni: your-domain.com
    verify: true
websocket:
    enabled: true
    path: /trojan-ws
    host: your-domain.com      # 客户端这一侧的 host 才是真正写进 Host 头的
shadowsocks:
    enabled: true
    method: AES-128-GCM
    password: ws-password
```

### IPv6 专用配置

针对纯 IPv6 网络环境的优化配置：

**服务端配置**：

```json
{
  "run_type": "server",
  "local_addr": "::",
  "local_port": 443,
  "remote_addr": "::1",
  "remote_port": 80,
  "password": ["your_password"],
  "ssl": {
    "cert": "fullchain.pem",
    "key": "privkey.pem",
    "sni": "[::1]"
  },
  "tcp": {
    "fast_open": true
  }
}
```

**客户端配置**：

```json
{
  "run_type": "client",
  "local_addr": "::1",
  "local_port": 1080,
  "remote_addr": "example.com",
  "remote_port": 443,
  "password": ["your_password"],
  "outbound_local_addr": "::",
  "tcp": {
    "fast_open": true
  }
}
```

---

## 构建指南

> 要求 Go 版本 >= 1.27

### 使用 Make

```shell
git clone https://github.com/Potterli20/trojan-go-fork.git
cd trojan-go-fork
make
make install  # 安装 systemd 服务等（可选）
```

### 使用 Go 直接编译

```shell
git clone https://github.com/Potterli20/trojan-go-fork.git
cd trojan-go-fork
go build -tags "full"
```

> 可通过 [go-install](https://github.com/Jrohy/go-install) 快速安装 Go 环境：
> ```shell
> source <(curl -L https://go-install.netlify.app/install.sh)
> ```

### 交叉编译

Go 支持通过环境变量进行交叉编译：

```shell
# 64 位 Windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags "full"

# Apple Silicon (macOS)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags "full"

# 64 位 Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags "full"
```

---

## 故障排查

### 连接失败

**症状**：客户端无法连接到服务端

**排查步骤**：

1. **检查端口监听**：
   ```bash
   netstat -tlnp | grep trojan
   # 或
   ss -tlnp | grep trojan
   ```

2. **验证证书配置**：
   - 确保 `ssl.cert` 和 `ssl.key` 路径正确
   - 检查证书域名与 SNI 是否匹配
   - 测试证书有效性：`openssl x509 -in cert.pem -text`

3. **检查防火墙规则**：
   ```bash
   # Linux
   sudo ufw status
   sudo iptables -L -n

   # 确保 UDP 443 端口开放（如启用 QUIC）
   ```

4. **查看日志**：
   ```json
   {
     "log_level": 0,
     "log_file": "/var/log/trojan-access.log"
   }
   ```
   `log_level` 是 int 且级别**是反的**（`0`=All 最啰嗦 … `5`=Off）。没有 `access-log` 这个键，
   日志文件是 `log_file`；JSON 用下划线、YAML 用连字符（`log-level` / `log-file`），
   两者都来自 `proxy/config.go`。

### QUIC 连接问题

**症状**：QUIC 隧道无法建立

**解决方案**（按命中概率排序）：

1. **先确认 QUIC 真的在栈里**——这是最常见的原因：`run-type: client` / `server` 的栈是硬编码的，
   不含 QUIC 层，只加一个 `quic:` 段不会启用它，必须用 `custom` 把 `quic` 写成节点。
   判据：日志里应有 `QUIC server listening on <addr>`（服务端）或
   `Dialing to <addr> with congestion=<算法>`（客户端）。一条都没有就说明栈里根本没有 QUIC。

2. **证书校验失败**（实测两类报错）：
   - `x509: certificate signed by unknown authority` → 自签证书 + `insecure: false`
   - `x509: certificate has expired or is not yet valid` → 证书过期
   注意 `quic` 节点和内层 `tls` 节点**各校验一次**，自签测试要同时放开 `quic.insecure: true`
   和两处 `ssl.verify: false`。

3. **ALPN 不匹配**：两端必须填同一个值（默认 `hq-29`）。实测症状是**快速失败**：
   `QUIC failed to dial | CRYPTO_ERROR 0x178 (remote): tls: no application protocol`

4. **UDP 路径不通 / 对端没监听**：症状完全不同——是**等满超时**后报
   `QUIC failed to dial | timeout: no recent network activity`。
   所以"秒失败"看 ALPN/证书，"卡住才失败"看 UDP 通路。
   另注意：`nc -uvz` 对 UDP 是"发个包就算成功"，测不出真实可达性；
   `openssl s_client` 是 TCP 的，打到只做 `ListenUDP` 的 quic 端口必然 `Connection refused`
   （errno=111），那不代表 QUIC 有问题。可靠判据只有日志：
   - 服务端出现 `QUIC server listening on <addr>` = 监听成功
   - 客户端出现 `Connection established to <addr>` = 隧道建成
   中间盒丢 UDP 时，换端口或走 TCP 降级（`config/cloudflare_tcp_fallback.yaml`）。

5. **换拥塞算法**（只有这四个，写别的会告警并回退 BBR）：
   ```yaml
   quic:
     congestion: reno   # bbr / brutal / force-brutal / reno
   ```

6. **Brutal 只在显式选中时才参与限速**，`bbr` / `reno` 下 `brutal-up/down` 不会被读取：
   ```yaml
   quic:
     brutal-up: 0     # 单位是 bps
     brutal-down: 0
   ```

### WebSocket 访问无效

**症状**：添加用户后 WebSocket 无法连接

**原因**：已修复（v0.12.0+），确保使用最新版本

**临时方案**：
```json
{
  "websocket": {
    "enabled": true,
    "path": "/ws",
    "host": "your-domain.com"
  },
  "ssl": {
    "fallback_addr": "127.0.0.1",
    "fallback_port": 80
  }
}
```

`ssl.fallback_addr` / `fallback_port` 是 HTTP/1.1 回退地址（JSON 不支持注释，说明写在这里）。

### 性能不佳

**症状**：速度慢、延迟高

**优化建议**：

1. **启用 TCP Fast Open**：
   ```json
   {
     "tcp": {
       "fast_open": true
     }
   }
   ```

2. **调整 QUIC 窗口大小**（高带宽低延迟网络）：
   ```yaml
   quic:
     initial-stream-window: 262144   # 256KB
     initial-conn-window: 262144
   ```
   注意这几项要写在 **quic 节点自己的 `config` 块**里（和 `remote-addr` 同级），
   custom 模式下顶层的 `quic:` 不会传到隧道节点上。

3. **启用多路复用**（高并发场景）：
   ```json
   {
     "mux": {
       "enabled": true,
       "idle_timeout": 30,
       "concurrency": 8
     }
   }
   ```

4. **使用 BBR 拥塞控制**：
   ```yaml
   quic:
     congestion: bbr
   ```

### 优雅关闭超时

**症状**：服务停止需要数秒才能完全退出

**说明**：正常情况**不该**这样。空闲连接下 SIGTERM 停机是毫秒级的（本轮 QUIC 端到端实测：
客户端与服务端各 0.002s 退出）。`proxy/proxy.go` 的 `shutdownTimeout`（5s）只是**单个环节的上限**，
用来兜住"对端半开、中继卡死"的情况——真等到 5s 说明有连接没退干净，而不是设计如此。

**排查**：把 `log-level` 设为 `0`，看关闭阶段打出的是哪个受限环节
（`wait for relay loops to finish` / `close tunnels`，见 `proxy/proxy.go:218,225`），
再顺着那条连接找对端状态。

### 定位协程/内存泄露（`-diag-bind`，Go 1.27 特性）

**默认完全关闭**：不给 `-diag-bind` 就不开任何端口。给了才监听，而且**只接受回环地址**
（`:6060`、`0.0.0.0:6060` 这类会被直接拒绝——pprof 暴露协程栈与堆直方图，挂到公网等于把进程交出去）。

```bash
./trojan-go-fork -config config.json -diag-bind 127.0.0.1:6060
```

Go 1.27 把 `goroutineleak` 剖析正式开放给 `runtime/pprof`：它会先跑一轮带泄露检测的 GC，
**只**输出"阻塞在 channel/mutex 上、且没有任何活对象能引到"的协程。长期跑的代理用
`runtime.NumGoroutine()` 数总量区分不了"在途会话"和"泄露"，而这个可以直接点名：

```bash
curl 'http://127.0.0.1:6060/debug/pprof/goroutineleak?debug=1'   # 泄露的阻塞协程
curl 'http://127.0.0.1:6060/debug/pprof/heap?debug=1'             # 堆按分配点聚合
curl 'http://127.0.0.1:6060/debug/pprof/'                         # 其余剖析（含 profile/trace）
```

测试侧同一个判据已经接进 `test/scenario/goroutineleak_test.go`：`TestProbeIsNotVacuous`
先种一条必然泄露的阻塞协程自证判据有效，`TestRealStackLeavesNoLeakedGoroutines` 再用
socks+shadowsocks+mux 的真实栈比对关停前后的泄露栈增量。

---

## 图形界面客户端

Trojan-Go Fork 服务端兼容所有原版 Trojan 客户端（如 Igniter、ShadowRocket 等）。以下为支持扩展特性（WebSocket / Mux 等）的客户端：

- [Qv2ray](https://github.com/Qv2ray/Qv2ray)：跨平台客户端，支持 Windows / macOS / Linux，使用 Trojan-Go 核心。
- [Igniter-Go](https://github.com/p4gefau1t/trojan-go-android)：Android 客户端，Fork 自 Igniter，支持所有 Trojan-Go 扩展特性。

---

## 致谢

本项目基于以下优秀开源项目构建：

- [Trojan](https://github.com/trojan-gfw/trojan)
- [V2Fly](https://github.com/v2fly)
- [utls](https://github.com/refraction-networking/utls)
- [smux](https://github.com/xtaci/smux)
- [go-tproxy](https://github.com/LiamHaworth/go-tproxy)

如遇到配置或使用问题、发现 bug，或有更好的想法，欢迎加入 [Telegram 交流群](https://t.me/trojan_go_chat)。
