# Trojan-Go-Fork 配置说明

## 概述

Trojan-Go-Fork 支持 JSON 和 YAML 两种配置文件格式。配置文件通过 `run_type` 字段区分客户端和服务端模式。

## 基础配置

| 字段 | 类型 | 说明 |
|------|------|------|
| `run_type` | string | 运行模式：`client` 或 `server` |
| `local_addr` | string | 本地监听地址 |
| `local_port` | int | 本地监听端口 |
| `remote_addr` | string | 远程服务器地址（客户端）或目标地址（服务端） |
| `remote_port` | int | 远程服务器端口（客户端）或目标端口（服务端） |
| `password` | []string | 密码数组，支持多用户认证 |

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
| `max_idle_timeout` | int | 最大空闲超时（秒），保活周期取其一半 | 30 |
| `max_incoming_streams` | int | 最大并发流数 | 100 |
| `initial_stream_window` | int | 初始流窗口；0 = 交给 quic-go 库内默认（512KB） | 0 |
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
| `sqlite` | `statistic/memory` | 给一个路径即启用 SQLite 持久化（无独立配置段，也没有 `statistic.type` 这种键）；平台限制见 `statistic/sqlite` |
| `MaxIPPerUser` | `statistic/memory` | 每用户并发 IP 上限，默认 0；注意它的大小写就是这个拼法 |
| `mysql` | `statistic/mysql` | MySQL 后端整段 |
| `api` | `api/service` | gRPC 管理接口：`enabled` / `api_addr` / `api_port` / `ssl`（**没有** user/passwd） |
| `redirect_client_ip` | `redirector` | 是否把客户端地址注入重定向请求（`X-Forwarded-For` / `X-Real-IP`），默认关 |
| `udp_timeout` | `tunnel/socks`、`tunnel/dokodemo`、`tunnel/tproxy` | UDP 会话空闲超时（秒），三处默认都是 60 |
| `target_addr` / `target_port` | `tunnel/dokodemo` | 固定目标 |
| `trust_headers` / `disable_http_check` / `record_capacity` | `tunnel/trojan` | trojan 入站行为 |

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
