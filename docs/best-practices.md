# 部署与客户端最佳实践

这份文档回答两类最常见的问题：**服务端前面那台 nginx 到底该放什么**，以及**客户端
（尤其 Clash / mihomo）怎么配**。所有配置键都以 `docs/configuration.md` 与代码里的
结构体为准，示例会随仓库一起被 CI 实喂给二进制解析。

## 1. 先分清两种拓扑

| 拓扑 | 谁在 443 | 适用场景 | 参考 |
|------|----------|----------|------|
| A. 直连 | trojan-go | 有公网 IP、能直接开 443；nginx 只在 80 当诱饵 | 本文第 2~4 节 |
| B. 前置反代 / CDN | nginx 或 CDN | 需要 WebSocket、隐藏源站、套 CDN | README 的「CDN 中转配置」 |

两种拓扑不能混着配。最常见的错误是：nginx 已经在 443 上终结 TLS，又让 trojan-go
也监听 443 / 或者用明文 `http://` 回源到 trojan-go 的 TLS 端口。

下面以拓扑 A 为主——也就是 issue #103 里那份 compose 的形态。

## 2. 拓扑 A：trojan-go 独占 443，nginx 当诱饵

### 2.1 数据流

1. 客户端用**真实域名**做 TLS 握手（证书就是你的域名证书）。
2. 认证成功的流量按 trojan 协议转发。
3. 认证失败、或根本不是 trojan 的流量，被原样中继到 `remote-addr:remote-port`。
4. 因此 nginx 上必须**真的有一个站点在听**，否则探测者会看到 `connection refused`。

### 2.2 服务端配置

```yaml
run-type: server
local-addr: 0.0.0.0
local-port: 443
remote-addr: nginx        # compose 里的服务名即可；nginx 是诱饵站点
remote-port: 80
password:
    - your_strong_password
ssl:
    cert: /etc/v2ray/fullchain.pem
    key: /etc/v2ray/privkey.pem
    sni: example.com
    # 明文 HTTP 直接打到 443 时（TLS 握手都失败）也交给同一个诱饵站
    fallback-addr: nginx
    fallback-port: 80
    # 证书文件变化后每隔多少秒检查并热加载；0 = 不检查（默认）
    cert-check-rate: 60
```

三个容易漏的点：

- **`remote-addr` 必填。** 留空会拼出 `":80"`，实测 `net.Dial("tcp", ":80")` 连的是
  本机 `127.0.0.1`，认证失败的流量被代打到代理自己主机的服务，而且没有任何报错。
- **`ssl.fallback-port` 不是可选项。** 不配的话，明文 HTTP 探测会看到连接被直接关闭，
  与"正常 HTTPS 站点"的行为不一致；配了才会被中继到 nginx。
- **`cert-check-rate` 默认 0。** 想让证书续期自动生效就设一个正数（如 60），
  否则必须重启进程。

### 2.3 证书续期

推荐让 trojan-go 自己热加载，而不是给 certbot 挂 `docker.sock` 去重启容器：
`docker.sock` 等价于宿主机 root 权限，能不给就不给。

- `cert-check-rate: 60` 时，deploy hook 只需把新证书复制进 `/etc/v2ray`；
  下一轮检查发现字节变化就会重新加载，无需重启。
- 复制建议**先写临时文件再 `mv` 原子替换**，避免检查瞬间读到半个文件（读失败只会在
  下一轮重试，但原子替换更干净）。
- 保留了"重启容器"那套也能用，只是多了一次连接中断。

## 3. nginx 上放什么网页

目标只有一个：**让 `https://example.com`（以及 `http://example.com`）看起来像一个真的、
和你域名匹配的站点**。内容是什么不重要，重要的是不要有明显的"代理默认页"特征。

推荐程度从高到低：

1. **自建静态站**：Hugo / Astro / 纯 HTML 都行，内容与域名主题一致。
   体积小、没有登录口、没有可被扫描的后台。
2. **真实但无交互的页面**：公司主页、博客归档、文档站。
3. **反代一个真实站点**：可行但麻烦——要改写 `Host`、处理重定向与混合内容，还会把你
   的带宽和合规责任绑到第三方站点上，不建议长期使用。
4. **不推荐**：nginx 默认欢迎页、空白的 403/404、所有路径统一跳登录页。
   这些是最常见的"这里其实是个代理"指纹。

nginx 最小配置（ACME 与诱饵站共存）：

```nginx
server {
    listen 80;
    server_name example.com;

    server_tokens off;
    autoindex off;

    # certbot webroot
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        root /var/www/site;
        index index.html;
        try_files $uri $uri/ =404;
    }

    # 不暴露隐藏文件
    location ~ /\. {
        deny all;
    }
}
```

静态站目录里至少放一个 `index.html` 和一个像样的 `404.html`。不要用 `return 301`
把所有未知路径都跳到首页——真实站点不会这样。

## 4. 客户端

trojan-go fork 的服务端兼容**标准 trojan 客户端**。它自己的 `mux` 是扩展能力，
标准客户端不会用；服务端对两种连接都接受，所以客户端不需要为兼容性做任何开关。

常见选择：

| 平台 | 客户端 |
|------|--------|
| Windows / macOS / Linux | Clash Verge Rev、Mihomo Party、v2rayN、sing-box、NekoBox |
| Android | Clash Meta for Android (CMFA)、v2rayNG、NekoBox |
| iOS | Shadowrocket、Stash、sing-box |
| 路由器 | OpenClash、ShellClash |

### 4.1 Clash / mihomo（直连 443）

```text
proxies:
  - name: "trojan-go"
    type: trojan
    server: example.com
    port: 443
    password: "your_strong_password"
    sni: example.com          # 必须与证书域名一致
    udp: true                 # 需要 UDP 时打开（trojan-go 支持 Associate）
    skip-cert-verify: false   # 有有效证书就不要跳过校验
    # client-fingerprint: chrome   # 你的 mihomo 版本支持时可选用

proxy-groups:
  - name: "PROXY"
    type: select
    proxies: ["trojan-go", "DIRECT"]

rules:
  - GEOIP,CN,DIRECT
  - MATCH,PROXY
```

### 4.2 Clash / mihomo（WebSocket，走 nginx/CDN）

仅当你按 README 的「CDN 中转配置」部署、服务端开了 `websocket` 时才需要：

```text
proxies:
  - name: "trojan-go-ws"
    type: trojan
    server: example.com
    port: 443
    password: "your_strong_password"
    sni: example.com
    udp: true
    network: ws
    ws-opts:
      path: "/trojan-ws"      # 必须与服务端 websocket.path 完全一致
      headers:
        Host: example.com
```

几个常见坑：

- `sni` 与证书不匹配会直接握手失败；用 IP 连接时更要注意。
- 服务端开了 `websocket` 但客户端没写 `network: ws`，会一直认证失败。
- 直连拓扑不要加 `network: ws`，反之亦然。
- 不要开 `skip-cert-verify` 来"解决"连接问题——那通常说明域名/SNI/证书配错了。

## 5. 安全清单

- 公网只暴露 80 / 443；MySQL、API 端口留在容器网络或内网。
- MySQL 用强口令；`users.password` 存的是 SHA224 哈希，不是明文。
- 管理 API 默认失败关闭：非回环监听必须配 `api.token` 或 TLS 客户端证书，
  否则拒绝启动；`api.allow_insecure: true` 只用于明确的内网场景。
- 需要限制单用户并发来源时用 `max_ip_per_user`（全局）或 `users.maxip`（单用户）。
- `redirect-client-ip` 默认关：打开意味着把客户端地址交给诱饵站那一方。
- 容器里的配置与证书按只读挂载；`.env` 不要进版本库。
- 日志级别别长期停在 `0`（AllLevel）：公网探测流量会把 debug 日志变成磁盘压力。

## 6. 上线后自检

```text
# 证书与 SNI
openssl s_client -connect example.com:443 -servername example.com </dev/null

# 诱饵站（80）
curl -I http://example.com/

# 错误密码：应当与正常站点表现一致，而不是 RST/connection refused
curl -k --max-time 5 https://example.com/ -H 'Host: example.com'
```

若第 2、3 条看到的是空响应或连接被重置，回到第 2.2 节检查
`remote-addr` / `ssl.fallback-addr` 以及 nginx 是否真的在监听。
