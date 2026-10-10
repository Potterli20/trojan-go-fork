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
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
)

// writeSelfSigned 生成一份自签证书写入 dir 并返回路径：NewServer 只接受磁盘上的
// cert/key 路径（tls.LoadX509KeyPair），不接受内存里的 tls.Certificate。
func writeSelfSigned(t *testing.T, dir string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("自签证书失败: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("导出私钥失败: %v", err)
	}

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writePEM := func(path string, typ string, b []byte) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatalf("写 %s 失败: %v", path, err)
		}
		defer f.Close() //gosec:disable -- 错误忽略：测试清理
		if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: b}); err != nil {
			t.Fatalf("编码 %s 失败: %v", path, err)
		}
	}
	writePEM(certPath, "CERTIFICATE", der)
	writePEM(keyPath, "EC PRIVATE KEY", keyDER)
	return certPath, keyPath
}

func quicServerCtx(t *testing.T, port int, certPath, keyPath string) context.Context {
	t.Helper()
	ctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		QUIC: QUICConfig{
			ALPN:               "hq-29",
			MaxIdleTimeout:     30,
			MaxIncomingStreams: 100,
			Congestion:         "bbr",
		},
	})
	return config.WithConfig(ctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{CertPath: certPath, KeyPath: keyPath},
	})
}

// TestServerCloseReleasesUDPPort 回归：NewServer 自己 net.ListenUDP 出来的 socket
// 交给 quic.Listen 后就不在手上，而 quic-go 的 Transport 只在 createdConn 为真时
// 才关它（transport.go:488），传入现成 PacketConn 时 Close 只设一次读截止。
// 于是 Server.Close() 谁也没关这个 socket：每个实例漏 1 个 fd，端口被一直占住，
// 同进程里重建监听就 bind 失败。判据用最直接 observable：关掉之后该端口必须能重新绑定。
func TestServerCloseReleasesUDPPort(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	port := common.PickPort("udp", "127.0.0.1")

	server, err := NewServer(quicServerCtx(t, port, certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Logf("Close 返回: %v", err)
	}

	ln, err := net.ListenPacket("udp", (&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}).String())
	if err != nil {
		t.Fatalf("端口 %d 在 Server.Close() 之后仍被占用，说明 UDP socket 泄漏: %v", port, err)
	}
	if err := ln.Close(); err != nil {
		t.Logf("清理监听失败: %v", err)
	}
}

// TestServerCloseIsIdempotent 与 adapter/transport 同型的不变量：共用下层的 Close
// 可能被调多次，第二次不能再泄漏或报错。
func TestServerCloseIsIdempotent(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	server, err := NewServer(quicServerCtx(t, common.PickPort("udp", "127.0.0.1"), certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("第二次 Close: %v", err)
	}
}
