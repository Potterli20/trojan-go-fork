# Trojan-Go-Fork 配置说明

## 概述

Trojan-Go-Fork 支持 JSON 和 YAML 两种配置文件格式。配置文件通过 `run_type` 字段区分客户端和服务端模式。

同一份配置文档会被喂给**所有已注册模块**（`config/AGENTS.md`），各模块结构体里没声明的键会被
`encoding/json` 静默丢弃——**键名写错、或者放错节，都不会报错**。本页的键名逐条对照代码写，
仓库的 `.github/check-config-keys.py` 会按 (父节, 键) 成对校验本页与示例配置。

- [基础配置](#基础配置)
- [SSL/TLS 配置 (ssl)](#ssltls-配置-ssl)
- [WebSocket 配置 (websocket)](#websocket-配置-websocket)
- [QUIC 配置 (quic)](#quic-配置-quic)
- [路由配置 (router)](#路由配置-router)
- [Mux 多路复用配置 (mux)](#mux-多路复用配置-mux)
- [TCP 配置 (tcp)](#tcp-配置-tcp)
- [ShadowSocks 配置 (shadowsocks)](#shadowsocks-配置-shadowsocks)
- [传输插件配置 (transport_plugin)](#传输插件配置-transport_plugin)
- [出站控制 (freedom)](#出站控制-freedom)
- [中继缓冲区 (relay_buffer_*)](#中继缓冲区-relay_buffer_)
- [其它顶层键](#其它顶层键)
- [日志 (log_level / log_file)](#日志-log_level--log_file)
- [用户与流量后端](#用户与流量后端)
- [管理接口 (api)](#管理接口-api)
- [custom 模式的节点与路径 (inbound / outbound)](#custom-模式的节点与路径-inbound--outbound)
- [完整配置示例](#完整配置示例)
- [使用说明](#使用说明)

## 基础配置

| 字段 | 类型 | 说明 |
|------|------|------|
| `run_type` | string | 运行模式：`client` / `server` / `forward` / `nat` / `custom`（大小写不限，取值见下） |
| `local_addr` | string | 本地监听地址 |
| `local_port` | int | 本地监听端口 |
| `remote_addr` | string | 远程服务器地址（客户端）或目标地址（服务端） |
| `remote_port` | int | 远程服务器端口（客户端）或目标端口（服务端） |
| `password` | []string | 密码数组，支持多用户认证 |

`run_type` 是在 `proxy/proxy.go:471` 处 `ToUpper` 后查代理创建器注册表的，所以 `Client` 与
`client` 等价；写注册表里没有的值直接报 `unknown proxy type: <值>` 并终止启动。五个合法值对应的
语义：`client`（本地代理 → 上游 trojan 服务器）、`server`（trojan 入站 → 本地/Web 目标）、
`forward`（透明转发，按目标地址直连）、`nat`（UDP 端口保持的 NAT 转发，仅 Linux）、
`custom`（自己用 `inbound` / `outbound` 拼栈，见「custom 模式」一节）。可用哪些值还取决于构建标签
（`client` / `server` / `forward` / `nat` 的标签都是 `xxx || full || mini`，`custom` 是
`custom || full`，即 `mini` 里没有 `custom`；`nat` 的实现只在 Linux 编译得出来）。

### 客户端示例

```json
{
    "run_type": "client",
    "local_addr": "127.0.0.1",
    "local_port": 1080,
    "remote_addr": "your-server-ip-or-domain.com",
    "remote_port": 443,
    "password": ["your_secure_password_here"]
}
```

### 服务端示例

```json
{
    "run_type": "server",
    "local_addr": "0.0.0.0",
    "local_port": 443,
    "remote_addr": "127.0.0.1",
    "remote_port": 80,
    "password": ["your_secure_password_here"]
}
```

---

## SSL/TLS 配置 (ssl)

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `cert` | string | TLS 证书路径 | - |
| `key` | string | TLS 私钥路径 | - |
| `key_password` | string | 私钥密码（可选） | 空 |
| `sni` | string | SNI 主机名 | - |
| `server_name` | string | 服务器名称 | - |
| `verify` | bool | 是否验证服务端证书 | true |
| `verify_hostname` | bool | 是否验证主机名 | true |
| `cipher` | string | 加密套件（留空使用默认） | - |
| `reuse_session` | bool | 复用 TLS 会话（映射到 `SessionTicketsDisabled` 取反） | false |
| `alpn` | []string | ALPN 协议列表 | ["http/1.1"] |
| `curve_preferences` | []string | 密钥交换偏好（如 `X25519MLKEM768`、`X25519`）；留空用 Go 默认 | - |
| `fingerprint` | string | TLS 指纹（客户端常用 `chrome`） | - |
| `key_log` | string | TLS 密钥日志文件路径（用于调试） | - |
| `cert_check_rate` | int | 服务端定期重检证书的间隔（秒）；**0 表示不做定时重检** | 0 |
| `plain_http_response` | string | HTTP 响应文件（用于回落） | - |
| `fallback_addr` | string | 回落地址 | - |
| `fallback_port` | int | 回落端口 | 0 |

### 服务端 SSL 配置示例

```json
{
    "ssl": {
        "cert": "fullchain.pem",
        "key": "privkey.pem",
        "sni": "your-domain.com",
        "verify": false,
        "alpn": ["http/1.1"]
    }
}
```

### 客户端 SSL 配置示例

```json
{
    "ssl": {
        "sni": "your-domain.com",
        "verify": true,
        "verify_hostname": true,
        "fingerprint": "chrome",
        "alpn": ["http/1.1"]
    }
}
```

---

## WebSocket 配置 (websocket)

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否启用 WebSocket |
| `host` | string | WebSocket 主机名 |
| `path` | string | WebSocket 路径 |
| `headers` | object | 自定义 HTTP 请求头 |

### 示例

```json
{
    "websocket": {
        "enabled": true,
        "path": "/ws",
        "host": "your-domain.com",
        "headers": {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
        }
    }
}
```

---

## QUIC 配置 (quic)

**可达性**：QUIC 隧道只在 `run_type: custom` 下生效——`client` / `server` / `forward` / `nat`
的栈是硬编码的，不含 `QUIC` 层，光写 `quic` 段不会让隧道跑起来。并且 QUIC 节点的 `config`
块里必须自带 `ssl`（证书/SNI），因为每个节点只能看到自己的 ctx。

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `max_idle_timeout` | int | 最大空闲超时（秒）；客户端保活周期取其一半，服务端不生效 | 30 |
| `max_incoming_streams` | int | 最大并发流数 | 100 |
| `initial_stream_window` | int | 初始流窗口；0 = 交给 quic-go 库内默认（单流 2MB、连接 3MB） | 0 |
| `initial_conn_window` | int | 初始连接窗口；同上 | 0 |
| `alpn` | string | ALPN 标识（留空回落到 `hq-29`） | "hq-29" |
| `insecure` | bool | 是否跳过证书验证（仅测试） | false |
| `congestion` | string | 拥塞控制算法：`bbr`、`brutal`、`force-brutal`、`reno`；其它值告警并回退 BBR | "bbr" |
| `brutal_up` | uint64 | 上行速率，单位 **bps** | 0 |
| `brutal_down` | uint64 | 下行速率，单位 **bps** | 0 |

> **没有 `enabled` 这个键**（2026-10-11 删除：它带 `false` 默认值却全仓无人读，会让人误以为隧道默认关闭）。
> 另注：`brutal` 需要 `brutal_up` 和 `brutal_down` 同时 > 0（取二者较小值），`force-brutal` 只需要 `brutal_up`。

### 示例

`custom` 模式下的一个 outbound 节点：

```yaml
run-type: custom
outbound:
  node:
    - protocol: quic
      tag: quic
      config:
        remote-addr: 127.0.0.1      # 客户端语义：对端地址
        remote-port: 17443
        quic:
          alpn: hq-29
          max-idle-timeout: 30
          max-incoming-streams: 100
          congestion: bbr
        ssl:
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

服务端把 `remote-addr` / `remote-port` 当**监听地址**用（`net.ListenUDP(cfg.RemoteHost, cfg.RemotePort)`），并把 `ssl` 换成带 `cert` / `key` 的一组。

---

## 路由配置 (router)

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否启用路由 |
| `bypass` | []string | 绕过规则（直连） |
| `proxy` | []string | 代理规则 |
| `block` | []string | 屏蔽规则 |
| `domain_strategy` | string | 域名解析策略，取值见下 |
| `default_policy` | string | 默认策略（`proxy`、`bypass`、`block`） |
| `geoip` | string | GeoIP 数据库路径 |
| `geosite` | string | GeoSite 数据库路径 |

`domain_strategy` 的合法取值（`tunnel/router/client.go:333-339`，比较前会 `ToLower`，
下划线/连字符/连写三种拼法都接受）：

- `as_is` / `as-is` / `asis`
- `ip_if_non_match` / `ip-if-non-match` / `ipifnonmatch`
- `ip_on_demand` / `ip-on-demand` / `ipondemand`

**写别的值不会静默回退，而是启动即失败**：`default` 分支返回 `unknown strategy: <值>`。
注意没有 `ip_if_non_cname` 这一项（v2ray 里叫 `ip_if_non_match`）。

默认值（`RegisterConfigCreator`）：`default_policy = "proxy"`、`domain_strategy = "as_is"`、
`geoip` / `geosite` 为解析出的 `geoip.dat` / `geosite.dat` 路径。

### 规则格式

前缀共 7 种，逐个来自 `tunnel/router/client.go`（`loadCode` 的调用点）：

| 前缀 | 含义 |
|------|------|
| `geoip:<code>` | 按 GeoIP 数据库的国家/地区分组，如 `geoip:cn`、`geoip:private` |
| `geosite:<group>` | 按 GeoSite 数据库的站点分组，如 `geosite:cn`、`geosite:category-ads`、`geosite:geolocation-!cn` |
| `domain:<name>` | 域名匹配（后缀语义） |
| `full:<name>` | 域名完全匹配，如 `full:localhost` |
| `keyword:<word>` | 域名包含关键字 |
| `regexp:<expr>` | 域名正则匹配 |
| `cidr:<net>` | IP 段匹配，如 `cidr:192.168.1.1/24`；写错会报 `invalid cidr: <net>` |

`geoip:` / `geosite:` 依赖外部 `.dat` 文件（本仓库不打包数据）。路径解析顺序是
**绝对路径原样使用 → `$TROJAN_GO_LOCATION_ASSET` 目录下同名文件 → 程序所在目录**
（`common/common.go:GetAssetLocation`）；找不到时只跳过对应规则并告警，不会让进程启动失败。

### 示例

```json
{
    "router": {
        "enabled": true,
        "bypass": [
            "geoip:cn",
            "geoip:private",
            "geosite:cn",
            "geosite:private"
        ],
        "proxy": [
            "geosite:geolocation-!cn"
        ],
        "block": [
            "geosite:category-ads"
        ],
        "domain_strategy": "as_is",
        "default_policy": "proxy",
        "geoip": "/usr/share/trojan-go-fork/geoip.dat",
        "geosite": "/usr/share/trojan-go-fork/geosite.dat"
    }
}
```

---

## Mux 多路复用配置 (mux)

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `enabled` | bool | 是否启用多路复用 | false |
| `idle_timeout` | int | 空闲超时（秒） | 30 |
| `concurrency` | int | 并发连接数 | 8 |

### 示例

```json
{
    "mux": {
        "enabled": true,
        "idle_timeout": 30,
        "concurrency": 8
    }
}
```

---

## TCP 配置 (tcp)

同一个 `tcp` 路径被**两个模块**各自解析，可用字段不同：

`tunnel/freedom/config.go`（出站直连）：

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `prefer_ipv4` | bool | 优先 IPv4 | false |
| `keep_alive` | bool | TCP keepalive | true |
| `no_delay` | bool | 禁用 Nagle | true |
| `fast_open` | bool | TCP Fast Open | true |

`tunnel/transport/config.go`（入站监听）只有 `fast_open`（默认 true）。

> 因此 `no_delay` / `keep_alive` / `prefer_ipv4` 只作用于出站直连，写在配置里不会影响入站监听——
> 这不是配错了，是两个模块共用同一路径的既有设计（见仓库根 AGENTS.md 关于「同一份文档喂给所有
> 已注册模块」的说明）。

### 示例

```json
{
    "tcp": {
        "prefer_ipv4": false,
        "keep_alive": true,
        "no_delay": true,
        "fast_open": true
    }
}
```

---

## ShadowSocks 配置 (shadowsocks)

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否启用 ShadowSocks |
| `method` | string | 加密方法（如 `AES-128-GCM`） |
| `password` | string | ShadowSocks 密码 |

### 示例

```json
{
    "shadowsocks": {
        "enabled": false,
        "method": "AES-128-GCM",
        "password": ""
    }
}
```

---

## 传输插件配置 (transport_plugin)

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否启用传输插件 |
| `type` | string | 插件类型 |
| `command` | string | 插件命令路径 |
| `option` | string | 插件选项 |
| `arg` | []string | 插件参数 |
| `env` | []string | 环境变量 |

### 示例

```json
{
    "transport_plugin": {
        "enabled": false,
        "type": "",
        "command": "",
        "option": "",
        "arg": [],
        "env": []
    }
}
```

---

## 出站控制 (freedom)

`tunnel/freedom/config.go` 的顶层键：

| 字段 | 类型 | 说明 |
|------|------|------|
| `outbound_local_addr` | string | 出站拨号绑定的本地地址（指定出口 IP） |
| `outbound_fwmark` | int | 出站 socket 的 SO_MARK（Linux fwmark 策略路由） |
| `tcp` | object | 见上面 TCP 一节 |
| `forward_proxy` | object | 出站上游 SOCKS5 代理 |

`forward_proxy` 字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否走上游 |
| `proxy_addr` | string | 上游地址 |
| `proxy_port` | int | 上游端口 |
| `username` / `password` | string | 上游认证 |

配合 `router` 时，命中 `proxy` 规则的目标走上游，命中 `bypass` 的目标由 `DialConnDirect`
直连（`tunnel/freedom/client.go`、`tunnel/router/client.go:199`），即使 `forward_proxy` 已启用。

## 中继缓冲区 (relay_buffer_*)

`proxy/config.go` 的顶层键，控制中继 `io.CopyBuffer` 的池化 buffer：

| 字段 | 类型 | 默认值 | 上限 |
|------|------|--------|------|
| `relay_buffer_size` | int | 8192 | 1MB（超出则告警并按上限处理） |
| `relay_buffer_count` | int | 1024 | 65536 |

只在取值 > 0 时覆盖默认；超过上限只会增加常驻内存，不会提升中继性能，所以有告警兜底
（`proxy/proxy.go:402-412`）。

> UDP 路径不受 `relay_buffer_size` 调小影响：`tunnel/trojan` 的 `ReadWithMetadata` 在
> buffer 小于整包长度时会直接报错并断掉整条 packet 流，所以包路径仍会留够 `MaxPacketSize`
> （见 `proxy/proxy.go` 该处注释）。把 buffer 调小不会让 UDP 变坏，但也别指望它省内存。

## 其它顶层键

| 字段 | 归属 | 说明 |
|------|------|------|
| `log_level` | `proxy` | 日志级别（int，**数值越小越啰嗦**），默认 1；见「日志」一节 |
| `log_file` | `proxy` | 日志文件路径，留空写标准输出；见「日志」一节 |
| `sqlite` | `statistic/memory` | 给一个路径即启用 SQLite 持久化（无独立配置段，也没有 `statistic.type` 这种键）；平台限制见「用户与流量后端」 |
| `MaxIPPerUser` | `statistic/memory` | 每用户并发 IP 上限，默认 0 = 不限；注意它的大小写就是这个拼法 |
| `mysql` | `statistic/mysql` + `tunnel/trojan` | MySQL 后端整段；其中 `enabled` 由 trojan 入站读取用于选后端，见「用户与流量后端」 |
| `api` | `api/service` + `tunnel/trojan` | gRPC 管理接口整段（`enabled` / `api_addr` / `api_port` / `token` / `allow_insecure` / `ssl`）；非回环监听又没凭据时**拒绝启动**，见「管理接口」 |
| `redirect_client_ip` | `redirector` | 是否把客户端地址注入重定向请求（`X-Forwarded-For` / `X-Real-IP`），默认关 |
| `udp_timeout` | `tunnel/socks`、`tunnel/dokodemo`、`tunnel/tproxy` | UDP 会话空闲超时（秒），三处默认都是 60 |
| `target_addr` / `target_port` | `tunnel/dokodemo` | 固定目标 |
| `trust_headers` / `disable_http_check` / `record_capacity` | `tunnel/trojan` | trojan 入站行为 |
| `inbound` / `outbound` | `proxy/custom` | 只在 `run_type: custom` 生效，见「custom 模式」 |

## 日志 (log_level / log_file)

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `log_level` | int | 日志级别，**数值越小越啰嗦** | 1（InfoLevel） |
| `log_file` | string | 日志文件路径；留空则写**标准输出**（实测 stderr 一行都没有） | 空 |

级别取值来自 `log/log.go` 的常量块，顺序是反的：

| 值 | 级别 | 值 | 级别 |
|----|------|----|------|
| `-1` | Trace | `3` | Error |
| `0` | All / Debug（最啰嗦） | `4` | Fatal |
| `1` | Info | `5` | Off（完全静默） |
| `2` | Warn | | |

- 声明在 `proxy/config.go`，YAML 的拼法是 `log-level` / `log-file`。
- `log_file` 非空时以 `0600` 权限追加打开（`proxy/proxy.go:478-486`）。
- **只能填数字**：字段类型是 `int`。实测把 `log-level: info` 写进 YAML 会在启动期直接 FATAL，
  报 `failed to create proxy | yaml: unmarshal errors: line N: cannot unmarshal !!str "info"
  into int`（2026-10-13 复现）。仓库里的示例配置原先就有这一写法，已改回数字。
- 脱敏是"尽力而为"（`log/log.go` 的 `ObfuscateSensitiveData`）：短于 32 字符、又不带
  password/token 这类关键词的凭据**不会**被遮。要保密的凭据就别写进日志。

## 用户与流量后端

服务端用户来自顶层 `password` 数组，每个密码在内部换成 **SHA224 摘要的 56 个十六进制字符**（28
字节）作为用户标识（`common.SHA224String`，协议规定的算法，换不掉）。`-api list` 输出的 `hash`、
MySQL `users` 表里 `password` 列存的值，都是这个哈希串，不是明文密码。

**空密码会被拒绝**：SHA224("") 是公开常量，收下一个空密码等于多开一个免密用户，所以
`rejectEmptyPassword` 在 `AddUser` 入口就报 `refusing to use an empty password`
（`statistic/memory/memory.go:546-553`）。

后端与持久化由这三处决定，判定顺序在 `tunnel/trojan/server.go:58-66`——先看
`mysql.enabled`，为真用 MySQL 认证器，否则用 memory：

| 键 | 效果 |
|----|------|
| `sqlite` | 给一个文件路径 → memory 后端启用 SQLite 持久化，用户/流量重启不丢 |
| `mysql.enabled: true` | 改用 MySQL 后端，按 `check_rate` 周期与 `users` 表双向同步 |
| `MaxIPPerUser` | 每用户并发 IP 上限；`<= 0` 表示不限（`(*User).AddIP` 直接放行） |

> `mysql` 和 `api` 这两节由**两个模块各解析一次**：`tunnel/trojan` 的 Config 里只声明了
> `mysql.enabled` / `api.enabled`，整段其余字段由 `statistic/mysql` 与 `api/service` 各自的结构体读。
> 这是"同一份文档喂给所有已注册模块"的既有设计（见仓库根 `AGENTS.md`）。

### SQLite 持久化

- 没有独立配置节，也没有 `statistic.type` 这类开关，就是顶层 `sqlite` 一枚路径字符串。
- **不需要 build tag**：`statistic/memory` 无条件引用 `statistic/sqlite`，平台开关在
  `statistic/sqlite` 内部的文件名下。
- 只有 linux amd64/386/arm/arm64 有实现；其它平台在**启动期直接失败**并报
  `sqlite persistence is only supported on linux amd64/386/arm/arm64; current build does not
  include the driver`（`statistic/sqlite/qulite_nonlinux.go`）。旧版在这里静默降级成空实现，
  日志照样打"已启用持久化"，进程重启后数据全丢——现在宁可失败也不静默。
- 既没配 `sqlite` 也没配 `mysql` 时，启动日志会打一行
  `未配置持久化后端（sqlite/mysql），用户流量仅在内存中保存，进程退出后丢失`。

### MySQL 后端 (`mysql`)

需要构建包含 MySQL 后端：`component/mysql.go` 的标签是 `mysql || full || mini`，
所以 `full` 与 `mini` 默认就带着它，只有裁剪到别的东西时才需要显式 `-tags mysql`。

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `enabled` | bool | 是否改用 MySQL 认证后端 | false |
| `server_addr` | string | MySQL 主机 | 空 |
| `server_port` | int | MySQL 端口 | 3306 |
| `database` | string | 库名 | 空 |
| `username` / `password` | string | 账号密码 | 空 |
| `ca` | string | CA 文件路径；**非空才会**给 DSN 追加 `tls=custom` | 空 |
| `cert` / `key` | string | 客户端证书与私钥，**必须成对** | 空 |
| `check_rate` | int | 与 `users` 表同步的间隔（秒） | 30 |

代码里定死的行为（`statistic/mysql/mysql.go`）：

- `ca` 为空 ⇒ 完全不启用 TLS；`ca` 非空 ⇒ 强制 `MinVersion = TLS 1.2`。
- `cert` / `key` 只给一个 ⇒ 启动失败并报 `set both key and cert, or set neither`。
- `check_rate` ≤ 0 兜底成 30s（`time.NewTicker` 对非正周期会 panic）。
- 连接池固定 `MaxOpenConns=5` / `MaxIdleConns=2` / 连接最长存活 5 分钟。
- 启动时（`ensureUsersTable`）会幂等执行 `CREATE TABLE IF NOT EXISTS users (...)`，并对
  已存在的旧表补 `maxip` / `expire` 两列；表已存在且账号只有 DML 权限时建表语句会失败，
  此时只要关键列齐全就只告警、继续运行，否则启动失败并提示手动 ALTER。
  新建表以 `password` 列为主键（即要求哈希唯一）；已存在的旧表结构不动，请自行保证该列唯一，
  否则 `UPDATE ... WHERE password=?` 会命中多行、把同一份增量记到多行上。
- 每轮同步执行 `SELECT username,password,quota,download,upload,maxip,expire FROM users`，
  **这 7 列必须都在**；写回是 `UPDATE users SET upload=upload+?, download=download+? WHERE
  password=?`，匹配的是密码哈希。
- 写回时两个方向的取值是**刻意互换**的（`syncUsers` 里的原注释 "swap upload and download for
  users"）：`upload` 列累加的是 `recv`、`download` 列累加的是 `sent`。对着表里的数字排查时先记
  住这条，不然会觉得上下行反了（`statistic/mysql/mysql.go` 的 `syncUsers`）。
- `quota < 0` 视为不限流量；已用流量达到 quota 的用户会被从内存表里删掉（即拒绝认证）。
- 表里的 `maxip` 是**每用户**的并发 IP 上限，与顶层 `MaxIPPerUser`（对所有用户生效）是两条路径。
- 表里的 `expire` 是**到期时间**（Unix 秒级时间戳，`0` 或负数 = 永不过期）。到期后该用户会被拒绝
  认证：**MySQL** 后端在每轮同步时把它从内存移除（与 `quota` 用尽同一出口）；**SQLite** 后端在
  `AuthUser` 时直接拒绝、但不删库里的行（“到期”不等于“删除”，续期还要用这行）。SQLite 的 `expire`
  列由 `AutoMigrate` 自动补上，但值只在装载时读一次——改完库里的 `expire` 需要重启后端才生效。
  内存（配置 `password` 数组）用户没有到期概念，恒视为永久。
- 每轮写回失败时会把这轮已经从内存取走的流量**原样加回**（`RestoreTraffic`），不会因为一次
  数据库抖动就丢掉这段统计；库侧用户的增减由「DB→内存」的同步统一处理（不再依据 `RowsAffected`）。
- 同步中途 `db.Query` 出错时**整轮放弃**，不据此清理用户——所以 MySQL 抖动不会误删合法用户。

```json
{
    "password": ["your_secure_password_here"],
    "mysql": {
        "enabled": true,
        "server_addr": "127.0.0.1",
        "server_port": 3306,
        "database": "trojan",
        "username": "trojan",
        "password": "db_password",
        "ca": "",
        "cert": "",
        "key": "",
        "check_rate": 30
    }
}
```

## 管理接口 (`api`)

gRPC 管理接口在 `component/api.go` 里由 `api || full` 标签引入，**`mini` 不含**。

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `enabled` | bool | 服务端起管理 API；客户端起 `GetTraffic` 服务 | false |
| `api_addr` | string | 监听地址 | **无默认** |
| `api_port` | int | 监听端口 | **无默认** |
| `ssl.enabled` | bool | gRPC 走 TLS | false |
| `ssl.cert` / `ssl.key` | string | 服务端证书与私钥 | 空 |
| `ssl.verify_client` | bool | 要求并校验客户端证书（`RequireAndVerifyClientCert`） | false |
| `ssl.client_cert` | []string | 放行的客户端证书，PEM 路径列表，逐个 `AppendCertsFromPEM` | 空 |
| `token` | string | 共享密钥。非空时每个 RPC（含流式）都必须带 `authorization: Bearer <token>`，由 gRPC 拦截器常量时间比较；命令行客户端用 `-api-token` 传 | 空 |
| `allow_insecure` | bool | 失败关闭策略的显式逃生阀：非回环监听又没有任何凭据时，默认**拒绝启动**，写了这一行才放行（只 Warn） | false |

这一节以前没有任何凭据字段，gRPC API 自身也不带鉴权——能连上的人可以增删用户、尾随流量
记录。现在有两层可选凭据（`token`、`ssl.verify_client`），并且**默认失败关闭**：
`api.enabled: true` 且监听地址不是回环时，两者都没配就直接拒绝启动，错误文案会列出三条出路
（配 `api.token`、配 `api.ssl.enabled` + `verify_client`、或把 `api_addr` 写回回环地址），
要硬开就显式写 `api.allow_insecure: true`。这条检查在**建栈阶段同步**执行
（`tunnel/trojan.NewServer`/`NewClient` 的第一件事），所以它是非零退出码，不是日志里的一行
警告——放在 `api.RunService` 里返回错误是无效的，那个返回值在两个调用点都被丢进 goroutine。

注意 `api.ssl.enabled` 只加密、不鉴权：没开 `verify_client` 的话，任何能完成 TLS 握手的人
照样能调 API，所以那种组合仍然会被闸门拦下。

下面三条是 2026-10-13 用 `build/trojan-go-fork`（v0.10.0-4491）在 loopback 实测的结果：

1. **省略 `api_addr` / `api_port` 会把 API 开到所有网卡上**。`net.ResolveIPAddr("ip", "")` 返回
   通配地址，端口 0 由内核随机分配。实测一份只写 `"api": {"enabled": true}` 的配置起在
   `*:40127` 和 `*:40439` 两个随机端口（trojan server 实例每创建一个就调一次 `api.RunService`，
   `tunnel/trojan/server.go:482-485`）。**这个形态现在起不来了**：通配地址属于非回环监听，
   没有任何凭据就被闸门拒掉。要跨机管理就配 `token` 或 TLS 客户端证书，要在本机用就固定写
   `"api_addr": "127.0.0.1"`。
2. **API 起不来是静默的**。`api.RunService` 的返回值在 `s.wg.Go(...)` 里被丢弃。实测第二个进程
   的 `api_port` 被占用时，代理照常运行、日志里既没有 `server api failed to listen` 也没有别的
   报错。判断 API 到底在不在，只认这一行：`server-side api service is listening on <addr>`
   （客户端是 `client-side api service is listening on <addr>`）。
3. **`api.ssl.enabled: true` 之后内置 `-api` 命令行连不上**。`api/control` 固定用
   `insecure.NewCredentials()`（`api/control/control.go`），没有 TLS 客户端。实测报
   `rpc error: code = Unavailable ... error reading server preface: EOF`。要加密就只能自己写
   gRPC 客户端，或者让 API 只监听回环地址。`-api-token` 也是明文 metadata，所以"TLS + token"
   这个组合内置命令行给不了：跨机管理要么走隧道/防火墙把 API 限制成内网可达 + token，
   要么自己写带 TLS 的客户端。

### 服务与方法

定义在 `api/service/api.proto`（`api.pb.go` / `api_grpc.pb.go` 是生成物，改协议要跑
`api/service/gen.sh`，别手改）：

- `TrojanServerService`：`ListUsers` / `GetUsers` / `SetUsers` / `GetRecords` /
  `SetOutboundConfig` / `GetOutboundConfig`
- `TrojanClientService`：`GetTraffic`

### 内置命令行客户端 `-api`

| 参数 | 说明 |
|------|------|
| `-api` | 取 `record` / `list` / `get` / `set`；其它值报 `unknown api command: <值>` |
| `-api-addr` | 目标 API 地址，默认 `127.0.0.1:10000`；要和配置里的 `api_addr` + `api_port` 对上 |
| `-api-token` | 服务端 `api.token` 配了就必须传，否则每个 RPC 都返回 `Unauthenticated`；以 `authorization: Bearer <token>` 发出，一元与流式方法走同一条注入路径 |
| `-target-password` / `-target-hash` | 指定用户；只给明文密码时服务端自己算哈希（`Hash` 为空则 `SHA224String(Password)`，`api/service/server.go:105-107`） |
| `--add-profile` / `--modify-profile` / `--delete-profile` | `-api set` 的操作，三者必须选且只选一个，都不给报 `Invalid operation` |
| `--upload-speed-limit` / `--download-speed-limit` | 限速，单位字节/秒（令牌桶 burst 取该值的 2 倍），0 = 不限 |
| `--ip-limit` | 该用户的并发 IP 上限，0 = 不限 |

补充：

- `-api` 是 option handler（priority 50），命中就接管本次运行，不再起代理。
- `--add-profile` 走 `AddUser` + `SetKeyShare`（把明文密码交给认证器算哈希并登记 key share）。
  MySQL 后端下这两步**也会成功**——`statistic/mysql` 的 Authenticator 内嵌 `*memory.Authenticator`，
  方法是继承来的。但下一轮 `syncUsers` 会拿 `UPDATE ... WHERE password=?` 的影响行数判存在，
  ≠ 1 就 `DelUser`（`statistic/mysql/mysql.go:62-68`），所以**只通过 API 加的用户活不过一个
  `check_rate` 周期**；MySQL 模式下加用户要写 `users` 表。
- `-api set` 被服务端拒绝时**退出码非 0**（`api/control/control.go:157-164`），可以直接在脚本里判；
  `-api record` 是 server-streaming，流打完返回的 `io.EOF` 算成功。

```json
{
    "api": {
        "enabled": true,
        "api_addr": "127.0.0.1",
        "api_port": 10000
    }
}
```

## custom 模式的节点与路径 (`inbound` / `outbound`)

只有 `run_type: custom` 会读这两节（`proxy/custom/config.go`），`client` / `server` /
`forward` / `nat` 的栈是硬编码的，写 `inbound` / `outbound` 不生效。`custom` 需要
`custom || full` 标签（**不进 `mini`**）。

结构是「节点表 + 路径」两层：

| 字段 | 类型 | 说明 |
|------|------|------|
| `inbound.node` / `outbound.node` | []object | 节点表，每项 `{protocol, tag, config}` |
| `inbound.node[].protocol` | string | 隧道名，大小写不限（内部 `ToUpper` 后查注册表） |
| `inbound.node[].tag` | string | 这条路径里引用它用的名字 |
| `inbound.node[].config` | object | 该节点的配置，**按根层文档解析**（见下） |
| `inbound.path` | [][]string | 入站栈路径，可以有多条 |
| `outbound.path` | [][]string | 出站栈路径，**只允许恰好 1 条** |

三个容易踩的点：

- **节点 `config` 块是独立的一份文档**。实现把 `config` 重新 marshal 成 YAML 再交给
  `config.WithYAMLConfig`（`proxy/custom/custom.go:23-29`），所以块内的 `remote_addr` / `ssl` /
  `quic` 都是**根层键**，不能再套一层节点名；每个节点也只能看到自己的 ctx（QUIC 节点必须自带
  `ssl` 就是这个原因）。
- **`protocol` 名必须是注册过的隧道**，否则启动报 `invalid protocol name:<大写名>`。注册的是
  `ADAPTER`、`DOKODEMO`、`FREEDOM`、`HTTP`、`MUX`、`QUIC`、`ROUTER`、`SHADOWSOCKS`、
  `SIMPLESOCKS`、`SOCKS`、`TLS`、`TPROXY`、`TRANSPORT`、`TROJAN`、`WEBSOCKET`；其中
  `ADAPTER` / `TRANSPORT` 是框架内部层，`TPROXY` 与 `nat` 一样只在 Linux 有实现。
- **`outbound.path` 的长度必须是 1**，多写一条直接报
  `there must be only 1 path for outbound protocol stack`；`path` 里引用没定义过的 tag 报
  `invalid node tag: <tag>`。

`config/` 下的 `cloudflare_quic_client.yaml`、`quic_server_example.yaml` 等就是可用的 custom 样例。

## 完整配置示例
### 客户端完整配置 (client.json)

```json
{
    "run_type": "client",
    "local_addr": "127.0.0.1",
    "local_port": 1080,
    "remote_addr": "your-server-ip-or-domain.com",
    "remote_port": 443,
    "password": [
        "your_secure_password_here"
    ],
    "ssl": {
        "cert": "",
        "key": "",
        "key_password": "",
        "sni": "your-domain.com",
        "server_name": "",
        "verify": true,
        "verify_hostname": true,
        "cipher": "",
        "reuse_session": true,
        "alpn": [
            "http/1.1"
        ],
        "curve_preferences": [],
        "fingerprint": "chrome",
        "key_log": "",
        "cert_check_rate": 60,
        "plain_http_response": "",
        "fallback_addr": "",
        "fallback_port": 0
    },
    "tcp": {
        "fast_open": true
    },
    "websocket": {
        "enabled": true,
        "path": "/ws",
        "host": "your-domain.com",
        "headers": {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8"
        }
    },
    "quic": {
        "max_idle_timeout": 30,
        "max_incoming_streams": 100,
        "initial_stream_window": 0,
        "initial_conn_window": 0,
        "alpn": "hq-29",
        "insecure": false,
        "congestion": "bbr",
        "brutal_up": 0,
        "brutal_down": 0
    },
    "shadowsocks": {
        "enabled": false,
        "method": "AES-128-GCM",
        "password": ""
    },
    "transport_plugin": {
        "enabled": false,
        "type": "",
        "command": "",
        "option": "",
        "arg": [],
        "env": []
    },
    "router": {
        "enabled": true,
        "bypass": [
            "geoip:cn",
            "geoip:private",
            "geosite:cn",
            "geosite:private"
        ],
        "proxy": [
            "geosite:geolocation-!cn"
        ],
        "block": [
            "geosite:category-ads"
        ],
        "domain_strategy": "as_is",
        "default_policy": "proxy",
        "geoip": "/usr/share/trojan-go-fork/geoip.dat",
        "geosite": "/usr/share/trojan-go-fork/geosite.dat"
    },
    "mux": {
        "enabled": true,
        "idle_timeout": 30,
        "concurrency": 8
    }
}
```

### 服务端完整配置 (server.json)

```json
{
    "run_type": "server",
    "local_addr": "0.0.0.0",
    "local_port": 443,
    "remote_addr": "127.0.0.1",
    "remote_port": 80,
    "password": [
        "your_secure_password_here"
    ],
    "ssl": {
        "cert": "fullchain.pem",
        "key": "privkey.pem",
        "key_password": "",
        "sni": "your-domain.com",
        "server_name": "",
        "verify": false,
        "verify_hostname": true,
        "cipher": "",
        "reuse_session": true,
        "alpn": [
            "http/1.1"
        ],
        "curve_preferences": [],
        "fingerprint": "",
        "key_log": "",
        "cert_check_rate": 60,
        "plain_http_response": "",
        "fallback_addr": "",
        "fallback_port": 0
    },
    "tcp": {
        "fast_open": true
    },
    "websocket": {
        "enabled": false,
        "path": "/ws",
        "host": "your-domain.com",
        "headers": {}
    },
    "quic": {
        "max_idle_timeout": 30,
        "max_incoming_streams": 100,
        "initial_stream_window": 0,
        "initial_conn_window": 0,
        "alpn": "hq-29",
        "insecure": false,
        "congestion": "bbr",
        "brutal_up": 0,
        "brutal_down": 0
    },
    "shadowsocks": {
        "enabled": false,
        "method": "AES-128-GCM",
        "password": ""
    },
    "transport_plugin": {
        "enabled": false,
        "type": "",
        "command": "",
        "option": "",
        "arg": [],
        "env": []
    },
    "router": {
        "enabled": true,
        "bypass": [],
        "proxy": [],
        "block": [
            "geoip:private"
        ],
        "domain_strategy": "as_is",
        "default_policy": "proxy",
        "geoip": "/usr/share/trojan-go-fork/geoip.dat",
        "geosite": "/usr/share/trojan-go-fork/geosite.dat"
    },
    "mux": {
        "enabled": false,
        "idle_timeout": 30,
        "concurrency": 8
    }
}
```

---

## 使用说明

### 启动命令

```bash
# 使用默认配置文件
./trojan-go-fork

# 指定配置文件
./trojan-go-fork -config client.json

# 使用 YAML 配置
./trojan-go-fork -config client.yaml
```

### 客户端场景

1. 设置 `run_type` 为 `client`
2. 配置本地监听地址和端口（如 `127.0.0.1:1080`）
3. 配置远程服务器地址和端口
4. 设置密码（需与服务端一致）
5. 配置 SSL/TLS（客户端需设置 `fingerprint` 为 `chrome` 等常用浏览器指纹）
6. 可选启用 WebSocket（适用于绕过防火墙）

### 服务端场景

1. 设置 `run_type` 为 `server`
2. 配置监听地址和端口（通常为 `0.0.0.0:443`）
3. 配置远程目标地址（如 `127.0.0.1:80`）
4. 配置 SSL 证书和私钥路径
5. 设置密码（支持多个密码）
6. 可选配置回落机制（`fallback_addr`）
