# Trojan-Go Fork

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.22-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)

> **注意**：本项目已通过 AI 辅助修复和修改，当前服务端功能正常，客户端可能存在部分问题。欢迎提交 PR 协助修复。

Trojan-Go Fork 是基于 [p4gefau1t/trojan-go](https://github.com/p4gefau1t/trojan-go) 的社区维护分支。由于原项目已停止维护，本 Fork 持续修复 bug、合并社区贡献并改进功能。

---

## 目录

- [与原版的差异](#与原版的差异)
- [Docker 部署](#docker-部署)
- [快速开始](#快速开始)
- [特性介绍](#特性介绍)
  - [可移植性](#可移植性)
  - [简易配置](#简易配置)
  - [WebSocket](#websocket)
  - [多路复用](#多路复用)
  - [路由模块](#路由模块)
  - [AEAD 加密](#aead-加密)
  - [传输层插件](#传输层插件)
- [构建指南](#构建指南)
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

### 3. URL 模式（客户端）

```shell
./trojan-go-fork -url 'trojan-go://password@example.com/?type=ws&path=%2Fpath&host=your-site.com'
```

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
"websocket": {
    "enabled": true,
    "path": "/your-websocket-path",
    "hostname": "www.your-awesome-domain-name.com"
}
```

`hostname` 可以省略，但服务端和客户端的 `path` 必须一致。开启 WebSocket 后，服务端可同时兼容 WebSocket 和标准 Trojan 流量。

> 注意：标准 Trojan 不支持 WebSocket。如需使用 WebSocket 承载流量，请确保通信双方均使用 Trojan-Go Fork。

### 多路复用

Trojan-Go Fork 支持基于 [smux](https://github.com/xtaci/smux) 的多路复用，通过单条 TLS 连接承载多条 TCP 连接，减少 TLS 握手延迟，提升高并发场景下的性能。

> 启用多路复用不会提高单链路的测速，但能显著降低大量并发请求时的延迟，例如浏览包含大量图片的网页。

客户端启用多路复用：

```json
"mux": {
    "enabled": true
}
```

只需在客户端启用即可，服务端会自动检测并适配。

### 路由模块

客户端内置路由模块，支持自定义分流策略。支持三种策略：

| 策略 | 说明 |
|------|------|
| `proxy` | 代理：通过 TLS 隧道由服务端连接目标 |
| `bypass` | 绕过：本地直接连接目标 |
| `block` | 封锁：直接关闭连接 |

配置示例：

```json
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
```

### AEAD 加密

支持基于 Shadowsocks AEAD 对 Trojan 协议流量进行二次加密，确保 WebSocket 传输流量不被不可信的 CDN 识别：

```json
"shadowsocks": {
    "enabled": true,
    "password": "my-password"
}
```

> 服务端和客户端必须同时开启并使用相同的密码。

### 传输层插件

支持可插拔的传输层，兼容 Shadowsocks [SIP003](https://shadowsocks.org/en/wiki/Plugin.html) 标准的混淆插件。以下为使用 `v2ray-plugin` 的示例：

> **注意**：此配置仅作演示用途，不保证安全性。

**服务端**：

```json
"transport_plugin": {
    "enabled": true,
    "type": "shadowsocks",
    "command": "./v2ray-plugin",
    "arg": ["-server", "-host", "www.baidu.com"]
}
```

**客户端**：

```json
"transport_plugin": {
    "enabled": true,
    "type": "shadowsocks",
    "command": "./v2ray-plugin",
    "arg": ["-host", "www.baidu.com"]
}
```

---

## 构建指南

> 要求 Go 版本 >= 1.22

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
