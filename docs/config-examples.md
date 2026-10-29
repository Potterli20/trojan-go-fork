# 配置示例

## Cloudflare HTTP/3 配置

> **先读这条**：下面两份配置是 `custom` 模式，不是 `client` / `server`。QUIC 只在 `custom` 下可达
> （内置运行类型的栈里没有 QUIC 层），`quic.enabled` 这个键也不存在。能在这里核实的是
> trojan-go ↔ trojan-go 的 QUIC 隧道；至于公网 QUIC 能否抵达你的服务端，取决于 UDP 直通 /
> Spectrum 的 UDP 转发，那不属于本仓库的代码。

从 README 的[配置示例](../README.md#配置示例)拆出来的三份完整可跑配置，每一段都会被 CI 实喂给真实二进制校验。

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

## CDN 中转配置

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

两个都为空时**拒绝启动**。留空过去会拼出 `":80"` 这样的地址，而 `net.Dial("tcp", ":80")`
连的是 **127.0.0.1**——认证失败的流量会被代打到代理自己主机的本地服务，配置写错却没有任何
症状。trojan 服务端同样要求 `remote-addr`（`shadowsocks`/`quic` 早就这么拦了）。

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

## IPv6 专用配置

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
