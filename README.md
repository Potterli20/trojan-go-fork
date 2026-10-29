# Trojan-Go Fork

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.27-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)

> **注意**：本项目已通过 AI 辅助修复和修改，当前服务端功能正常，客户端可能存在部分问题。欢迎提交 PR 协助修复。

Trojan-Go Fork 接手已停止维护的 [p4gefau1t/trojan-go](https://github.com/p4gefau1t/trojan-go)，继续修 bug、合并社区贡献、加固并发与资源行为。只有 `master` 一个长期分支；来源链、发布标签与移植状态见[项目来源与分支](#项目来源与分支)。

---

## 目录

- [项目来源与分支](#项目来源与分支)
- [与原版的差异](#与原版的差异)
- [快速开始](#快速开始)
- [Docker 部署](#docker-部署)
- [特性一览](#特性一览)
- [配置示例](#配置示例)
- [构建指南](#构建指南)
- [故障排查](#故障排查)
- [部署与客户端最佳实践](docs/best-practices.md)
- [图形界面客户端](#图形界面客户端)
- [致谢](#致谢)

---

## 项目来源与分支

- **血统**：上游 [p4gefau1t/trojan-go](https://github.com/p4gefau1t/trojan-go) 已停止维护，`master` 最后一次提交是 2021-09-14 的 `2dc60f52`；[fregie/trojan-go](https://github.com/fregie/trojan-go) 是它的 GitHub fork，接了一批修复；本仓库 2021-12-12 单独建仓，导入上游完整历史后接着做，Release 与镜像都从这里出。
- **本仓库 `fork = false`**：不是 GitHub 网络 fork，而是把历史推到一个新仓库里继续维护，所以 fork 图和 "Compare with upstream" 页面都看不到这条血缘。判血统要看 git 历史——`2dc60f52` 是本仓库 `master` 的祖先，这才是"基于 p4gefau1t/trojan-go"的依据。
- **分支只有 `master`**：默认分支，也是唯一长期分支（`git ls-remote --heads origin` 只列出它）。功能分支合入即删，不留 topic 分支。

```bash
git clone https://github.com/Potterli20/trojan-go-fork.git   # 默认就是 master
git checkout V2026.10.25                                     # 或者锁定到某一次发布
```

发布标签是 `V` 加发布日期，至今两百多个；Docker 侧另有不可变的 `sha-<提交号前 8 位>` 标签，配合 digest 才能回捞任意一次构建，取法见[取回历史构建产物](#取回历史构建产物)。CI 各工作流的触发口径、以及 fregie 的分支还差哪些提交没合入，写在 [docs/branches.md](docs/branches.md)。

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

配套开关 `-url-option` 只认三个键：`mux` / `listen` / `api`（写第四种键会被拒绝启动，
过去是无声忽略——`listne=...` 这类拼错会让你以为改了监听地址，实际用的还是默认值），默认值是
`mux=true;listen=127.0.0.1:1080`——**不写 `-url-option` 也会开多路复用**，想关掉要显式传：

```shell
./trojan-go-fork -url 'trojan-go://password@example.com/' -url-option 'mux=false;listen=127.0.0.1:1080'
```

`mux` 的值不是合法 bool 时报 `invalid mux value: <值>`；键值对之间用 `;` 分隔，少写 `=`
报 `invalid option format: <pair>. expected key=value`（两条错误文案都实测过）。尾随的 `;`
与 `;;` 只当空段跳过——一个纯书写习惯不该把一次可用的启动判成硬失败。

---

## Docker 部署

预构建的 Docker 镜像可从以下仓库获取：

- **Docker Hub**：[trliwork/trojan-go-fork](https://hub.docker.com/r/trliwork/trojan-go-fork/tags)
- **GitHub Container Registry**：[ghcr.io/potterli20/trojan-go-fork](https://github.com/Potterli20/trojan-go-fork/pkgs/container/trojan-go-fork)

镜像标签的含义（决定你能不能按次回捞某一次构建）：

| 标签 | 由谁产生 | 是否可变 |
|---|---|---|
| `latest` | 打 tag 的正式发布 | 可变，指向最近一次发布 |
| `V2026.10.25` | 每周发布工作流的 tag | 不变，一枚对应一次发布 |
| `snapshot` | 每次推送到 master | **可变**，每次 push 覆盖上一份 |
| `sha-1a2b3c4d` | 每一次构建（master 与 tag 都打） | 不变，对应一次具体提交 |

`ghcr.io` 上这个包目前是**私有**的：匿名 `docker pull` 与列标签都会 401，需要用一个带
`read:packages` 权限的令牌先 `docker login ghcr.io -u <你的用户名>`。Docker Hub 那一份是公开的。

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

## 特性一览

协议与配置文件完全兼容原版 Trojan。下表这些扩展特性需要**通信双方都使用 Trojan-Go Fork**，
每一项的配置键、默认值和已知坑都在 [特性详解](docs/features.md) 里逐条展开。

| 特性 | 一句话 |
| --- | --- |
| [可移植性](docs/features.md#可移植性) | 单文件无依赖，默认关 CGO |
| [简易配置](docs/features.md#简易配置) | 一个 `run_type` 加少量键即可启动 |
| [WebSocket](docs/features.md#websocket) | TLS + WS 承载，可走 CDN 中转 |
| [HTTP/2 代理入口（h2c）](docs/features.md#http2-代理入口h2c) | 明文 HTTP/2 入站 |
| [重定向（诱饵）与客户端地址注入](docs/features.md#重定向诱饵与客户端地址注入) | 认证失败时把探测者送去诱饵站 |
| [HTTP/3 QUIC 隧道](docs/features.md#http3-quic-隧道) | UDP 承载，可选 BBR / brutal 整形 |
| [多路复用](docs/features.md#多路复用) | smux，单条 TLS 跑多条 TCP |
| [路由模块](docs/features.md#路由模块) | geosite / geoip 分流（`.dat` 自备） |
| [AEAD 加密](docs/features.md#aead-加密) | 叠一层 shadowsocks 加密 |
| [传输层插件](docs/features.md#传输层插件) | simple-obfs / v2ray-plugin 风格混淆 |
| [管理接口与流量后端](docs/features.md#管理接口与流量后端) | gRPC API + memory / SQLite / MySQL |

---

## 配置示例

三份完整可跑的配置在 [docs/config-examples.md](docs/config-examples.md)：

- [Cloudflare HTTP/3 配置](docs/config-examples.md#cloudflare-http3-配置) —— 注意是 `custom` 模式，QUIC 只在 `custom` 下可达
- [CDN 中转配置](docs/config-examples.md#cdn-中转配置) —— 回源这一跳必须仍是 TLS 或 L4 直通
- [IPv6 专用配置](docs/config-examples.md#ipv6-专用配置)

仓库里的 [config/](config/) 与 [example/](example/) 另有成套模板；这些文档与示例中的每一段
配置都会被 CI 逐份喂给真实二进制解析，编造的键或写错的值类型都拦得住。

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

### 取回历史构建产物

Releases 页面每页只给 100 条，而本仓库从 `V2022.10.17` 起有 270 个 release（每个约 29 枚
跨平台资产）。要一次性把历史拿全，用 `scripts/archive-releases.sh`（依赖 `gh` 已登录 + `jq`；
归档镜像还需要 `skopeo`）：

```shell
# 只拿每个 release 的 linux-amd64 资产；不带 --assets-pattern 就是全部 *.zip
scripts/archive-releases.sh --assets-pattern '*linux-amd64*'

# 顺带把 ghcr 上的镜像标签全部存成多架构 oci-archive
scripts/archive-releases.sh --assets-pattern '*linux-amd64*' --images

# 只归档正式发布（跳过 snapshot 那类别名）
scripts/archive-releases.sh --images --image-tag-filter '^V'

# 先看会取哪些，不落盘
scripts/archive-releases.sh --dry-run
```

脚本按资产逐条下载、已存在的直接跳过，中断后重跑即可续传；产物落在 `archive/releases/<tag>/`
与 `archive/images/`。归档镜像务必用 `skopeo copy --all`：本镜像是 6 平台矩阵，`docker pull` +
`docker save` 只会存下本机那一个平台。要精确复原某一次构建，按 `sha-<短哈希>` 标签或该次构建
在 job summary 里记录的 digest 取，而不是按 `snapshot`——那个别名每次 push 都被覆盖。

---

## 故障排查

### 连接失败

**症状**：客户端无法连接到服务端

<details>
<summary>排查与解决</summary>

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

</details>
### QUIC 连接问题

**症状**：QUIC 隧道无法建立

<details>
<summary>排查与解决</summary>

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

</details>
### WebSocket 访问无效

**症状**：添加用户后 WebSocket 无法连接

<details>
<summary>排查与解决</summary>

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

</details>
### 性能不佳

**症状**：速度慢、延迟高

<details>
<summary>排查与解决</summary>

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

</details>
### 优雅关闭超时

**症状**：服务停止需要数秒才能完全退出
**说明**：正常情况**不该**这样。空闲连接下 SIGTERM 停机是毫秒级的（本轮 QUIC 端到端实测：

<details>
<summary>排查与解决</summary>

客户端与服务端各 0.002s 退出）。`proxy/proxy.go` 的 `shutdownTimeout`（5s）只是**单个环节的上限**，
用来兜住"对端半开、中继卡死"的情况——真等到 5s 说明有连接没退干净，而不是设计如此。

**排查**：把 `log-level` 设为 `0`，看关闭阶段打出的是哪个受限环节
（`wait for relay loops to finish` / `close tunnels`，见 `proxy/proxy.go:218,225`），
再顺着那条连接找对端状态。

</details>
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

Trojan-Go Fork 服务端兼容所有标准 Trojan 客户端，不要求客户端支持 mux 等扩展特性。
部署拓扑、诱饵站选型与 Clash / mihomo 的完整配置见
[部署与客户端最佳实践](docs/best-practices.md)。

- Windows / macOS / Linux：[Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev)、
  [Mihomo Party](https://github.com/mihomo-party-org/mihomo-party)、
  [v2rayN](https://github.com/2dust/v2rayN)、[sing-box](https://github.com/SagerNet/sing-box)
- Android：[Clash Meta for Android](https://github.com/MetaCubeX/ClashMetaForAndroid)、
  [v2rayNG](https://github.com/2dust/v2rayNG)、[NekoBox](https://github.com/MatsuriDayo/NekoBoxForAndroid)
- iOS：Shadowrocket、Stash、sing-box
- 路由器：OpenClash、ShellClash

需要支持 Trojan-Go 专有扩展（mux 等）时仍可用 [Igniter-Go](https://github.com/p4gefau1t/trojan-go-android)
与 Trojan-Go 自带客户端。

---

## 致谢

本项目基于以下优秀开源项目构建：

- [Trojan](https://github.com/trojan-gfw/trojan)
- [V2Fly](https://github.com/v2fly)
- [utls](https://github.com/refraction-networking/utls)
- [smux](https://github.com/xtaci/smux)
- [go-tproxy](https://github.com/LiamHaworth/go-tproxy)

如遇到配置或使用问题、发现 bug，或有更好的想法，欢迎加入 [Telegram 交流群](https://t.me/trojan_go_chat)。
