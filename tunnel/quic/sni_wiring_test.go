package quic

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
)

// ssl.sni / ssl.server_name 在 QUIC 客户端的接线判据。
//
// 缺陷形态（第 11 轮 D2）：quic client 的 tls.Config.ServerName 恒取 remote-addr，
// tlsCfg.TLS.SNI 赋给 Client.sni 后全仓零读取；configuration.md:85 把 server_name
// 列在通用 ssl 键表、quic_usage.md 示例在 QUIC 节点的 ssl 块里写了 sni，但两者都
// 静默无效——「按 IP 连接 + 域名证书」时校验对象是 IP 本身，证书没有 IP SAN 就
// 握手失败，用户毫无线索。修法与 tunnel/tls/client.go 同口径：sni 缺省回退
// remote-addr，server_name 缺省回退 sni，最终值既作 ClientHello 的 SNI 也作
// 证书校验名。
//
// 判据只造一张「只有 DNS SAN」的证书，让旧实现必然在 x509 校验处失败；
// 三条用例分别钉 sni 生效、防降级（接线绝不能靠关掉验证达成）、server_name 覆盖 sni。

// writeDomainSelfSigned 签一张只有 DNS SAN 的自签证书（刻意不带 IP SAN）：
// ServerName 一旦落在 IP 上，校验必以 "doesn't contain any IP SANs" 失败。
// 同 writeSelfSigned：NewServer 只收磁盘路径，所以这里也落盘。
func writeDomainSelfSigned(t *testing.T, dir, dnsName string) (certPath, keyPath string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("自签证书失败: %v", err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析证书失败: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("导出私钥失败: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	write := func(path, typ string, b []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatalf("写 %s 失败: %v", path, err)
		}
	}
	write(certPath, "CERTIFICATE", der)
	write(keyPath, "EC PRIVATE KEY", keyDER)
	return
}

// sniClientCtx 构造「按 IP 连接 + 验证开启」的客户端配置。
// QUICConfig.Insecure 用零值 false：证书校验必须开着，这正是本判据的前提。
func sniClientCtx(t *testing.T, port int, sni, serverName string) context.Context {
	t.Helper()
	cctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		QUIC: QUICConfig{
			ALPN:               "hq-29",
			MaxIdleTimeout:     30,
			MaxIncomingStreams: 100,
			Congestion:         "bbr",
		},
	})
	return config.WithConfig(cctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{Verify: true, SNI: sni, ServerName: serverName},
	})
}

func startSNIServer(t *testing.T, certPath, keyPath string) (*Server, int) {
	t.Helper()
	port := common.PickPort("udp", "127.0.0.1")
	server, err := NewServer(quicServerCtx(t, port, certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { server.Close() }) //gosec:disable -- 错误忽略：测试清理
	return server, port
}

func dialOnce(t *testing.T, client *Client) error {
	t.Helper()
	conn, err := client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName, DomainName: "example.com", Port: 443,
	}, nil)
	if err != nil {
		return err
	}
	return conn.Close()
}

func TestQUICClientConsumesSSLSNI(t *testing.T) {
	certPath, keyPath, cert := writeDomainSelfSigned(t, t.TempDir(), "quic.example.test")
	_, port := startSNIServer(t, certPath, keyPath)

	client, err := NewClient(sniClientCtx(t, port, "quic.example.test", ""), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理
	// ssl 配置段没有客户端 CA 键，信任锚在这里白盒注入；ServerName 的选择发生在
	// NewClient（建栈期），注入不触碰被测对象。
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client.tlsConfig.RootCAs = pool

	if err := dialOnce(t, client); err != nil {
		t.Fatalf("sni=域名 时按 IP 连接必须握手成功（校验对象必须是 sni 而不是 IP）: %v", err)
	}
}

func TestQUICClientSNIWrongNameIsRejected(t *testing.T) {
	certPath, keyPath, cert := writeDomainSelfSigned(t, t.TempDir(), "quic.example.test")
	_, port := startSNIServer(t, certPath, keyPath)

	client, err := NewClient(sniClientCtx(t, port, "other.example.test", ""), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client.tlsConfig.RootCAs = pool

	err = dialOnce(t, client)
	if err == nil {
		t.Fatal("sni 与证书不匹配时握手必须失败：接线绝不能靠关掉验证达成")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("失败原因必须指向证书校验，实际：%v", err)
	}
}

func TestQUICClientSNIRejectionDetectsDisabledVerification(t *testing.T) {
	// 防降级判据（TestQUICClientSNIWrongNameIsRejected）的变异对照替身：把
	// 「假修 = 建栈期把 InsecureSkipVerify 写死为 true」的拨号时配置状态在测试侧
	// 复现（quic-go 在拨号时克隆这份 tls.Config，两种写法产物完全一致；生产文件
	// 不落这种高危变异，由安全扫描器拦截），断言防降级判据在该状态下必然转红。
	certPath, keyPath, cert := writeDomainSelfSigned(t, t.TempDir(), "quic.example.test")
	_, port := startSNIServer(t, certPath, keyPath)

	client, err := NewClient(sniClientCtx(t, port, "other.example.test", ""), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client.tlsConfig.RootCAs = pool
	client.tlsConfig.InsecureSkipVerify = true // 复现假修的拨号时状态

	if err := dialOnce(t, client); err != nil {
		t.Fatalf("复现假修状态后拨号应当成功：防降级判据正是靠「此状态下不该成功」抓住这类假修: %v", err)
	}
}

func TestQUICClientConsumesSSLServerName(t *testing.T) {
	// configuration.md 的通用 ssl 键表里列了 server_name（TLS 客户端语义：覆盖 sni）。
	// 钉住「覆盖且生效」：sni 故意给错名字、server_name 给对——server_name 必须赢。
	certPath, keyPath, cert := writeDomainSelfSigned(t, t.TempDir(), "quic.example.test")
	_, port := startSNIServer(t, certPath, keyPath)

	client, err := NewClient(sniClientCtx(t, port, "wrong.example.test", "quic.example.test"), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client.tlsConfig.RootCAs = pool

	if err := dialOnce(t, client); err != nil {
		t.Fatalf("server_name 覆盖 sni 后必须握手成功: %v", err)
	}
}
