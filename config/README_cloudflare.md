# Cloudflare HTTP/3 配置说明

## 配置文件列表

### 1. `cloudflare_quic_client.yaml` - QUIC 客户端（推荐）
**用途**: 连接到 Cloudflare CDN，使用 HTTP/3 (QUIC) 协议  
**适用场景**: 需要绕过 QoS、高延迟网络、移动网络  
**特点**: 
- ✅ 启用 QUIC/HTTP3
- ✅ BBR 拥塞控制
- ✅ 优化流控参数

### 2. `cloudflare_quic_server.yaml` - QUIC 服务端
**用途**: 自建服务器，提供 QUIC 代理服务  
**适用场景**: 搭建自己的 Trojan 服务器  
**特点**:
- ✅ UDP 443 端口监听
- ✅ SQLite 持久化统计
- ✅ API 管理接口

### 3. `cloudflare_tcp_fallback.yaml` - TCP 降级备用
**用途**: QUIC 被阻断时的回退方案  
**适用场景**: 网络环境限制 UDP  
**特点**:
- ❌ 禁用 QUIC
- ✅ 传统 TCP + TLS 1.3

---

## 快速开始

### 步骤 1: 替换占位符

在所有配置文件中替换以下内容：

```yaml
# 密码
password:
  - your-strong-password-here    # 改为你的强密码

# 域名
remote-addr: your-domain.com     # 改为你的域名
sni: your-domain.com             # 必须与域名一致

# 证书路径（仅服务端需要）
key: /path/to/server.key         # 改为实际路径
cert: /path/to/server.crt        # 改为实际路径
```

### 步骤 2: 准备证书

#### 方式 A: Cloudflare 通用 SSL 证书
在 Cloudflare 后台设置：
1. **SSL/TLS** → **Overview** → 选择 **Full** 或 **Full (strict)**
2. **Edge Certificates** → 生成通用证书或使用自定义证书

#### 方式 B: 使用 Let's Encrypt
```bash
# 安装 certbot
sudo apt install certbot

# 获取证书
sudo certbot certonly --standalone -d your-domain.com

# 证书位置通常在：
# /etc/letsencrypt/live/your-domain.com/fullchain.pem
# /etc/letsencrypt/live/your-domain.com/privkey.pem
```

### 步骤 3: 启动服务

#### 客户端模式
```bash
./trojan-go -config config/cloudflare_quic_client.yaml
```

#### 服务端模式
```bash
./trojan-go -config config/cloudflare_quic_server.yaml
```

---

## 验证连接

### 检查 QUIC 是否生效

启动后查看日志输出：
```bash
# 期望看到类似日志
[QUIC] [conn=xxx] New connection accepted from x.x.x.x, congestion=bbr, alpn=[hq-29]
```

### 测试速度对比

```bash
# TCP 速度
curl -o /dev/null -w "%{time_total}\n" https://www.google.com

# QUIC 速度（需要 curl 支持 HTTP/3）
curl --http3 -o /dev/null -w "%{time_total}\n" https://www.google.com
```

### 检查 UDP 端口

```bash
# 确认 443 UDP 端口已监听
ss -uln | grep 443

# 预期输出类似：
# udp   LISTEN  0  128  0.0.0.0:443  0.0.0.0:*
```

---

## 故障排查

### Q1: QUIC 连接失败

**可能原因**:
- UDP 443 被防火墙拦截
- Cloudflare DNS 未正确配置
- 证书不匹配

**解决方案**:
```yaml
# 临时切换到 TCP 降级方案
quic:
  enabled: false
```

### Q2: 证书验证失败

**检查项**:
```bash
# 验证证书链
openssl s_client -connect your-domain.com:443 -servername your-domain.com

# 检查 SNI 是否匹配
grep sni config/cloudflare_quic_client.yaml
```

**临时测试**（仅开发环境）:
```yaml
ssl:
  insecure: true
```

### Q3: 性能不佳

**调优建议**:
```yaml
quic:
  # 增大窗口提升吞吐量
  initial-stream-window: 262144   # 256KB
  initial-conn-window: 262144
  
  # 增加并发
  max-incoming-streams: 200
  
  # 尝试不同拥塞算法
  congestion: cubic               # 从 bbr 切换
```

---

## Cloudflare 控制台设置

### 1. DNS 配置
- **Proxy Status**: 🧡橙色云朵（通过 CDN）或 ⚪灰色云朵（直连）
- **DNS Record Type**: A 或 AAAA

### 2. SSL/TLS 设置
- **Encryption Mode**: Full 或 Full (strict)
- **Minimum TLS Version**: 1.2
- **HTTP/3**: Enabled ✅

### 3. Advanced → Edge Certificates
- 启用 "Always Use HTTPS"
- 可选：启用 HSTS

---

## 性能对比

| 指标 | QUIC/HTTP3 | TCP+TLS |
|------|-----------|---------|
| 握手延迟 | ~1 RTT | ~2-3 RTT |
| 多路复用 | ✅ 原生 | ❌ 需额外处理 |
| 队头阻塞 | ✅ 避免 | ⚠️ 存在 |
| QoS 绕过 | ✅ 有效 | ❌ 无效 |
| 兼容性 | ⚠️ 依赖 UDP | ✅ 最好 |

---

## 安全建议

1. **始终使用有效证书** (`insecure: false`)
2. **启用 hostname 验证** (`verify-hostname: true`)
3. **使用强密码**（至少 16 位随机字符）
4. **定期更新证书**
5. **监控异常流量**（通过 API 接口）

---

## 参考资源

- [Cloudflare HTTP/3 文档](https://developers.cloudflare.com/http3/)
- [trojan-go-fork QUIC 使用指南](../../docs/quic_usage.md)
- [QUIC 拥塞控制最佳实践](https://quic.edm.uh.edu/)
