package tls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// handshakeWithConfig 用 rsa2048 证书（CN=localhost）起服务端，按给定客户端配置做一次
// TLS 握手，只返回 DialConn 的握手错误（nil = 握手成功）。
// 只用公开配置与 NewClient/DialConn 行为，不引用任何内部函数——恢复生产代码到修复前
// 照样能编译，判据才能真正对旧行为转红。
// 不依赖服务端 AcceptConn：那个调用会卡在 HTTP 嗅探读上（与握手无关），只用 DialConn
// 的返回值判定即可。
func handshakeWithConfig(t *testing.T, clientCfg *Config) error {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, []byte(rsa2048Cert), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(rsa2048Key), 0o600); err != nil {
		t.Fatal(err)
	}

	// 服务端不校验客户端 SNI：下面故意用不匹配的 SNI，若服务端开 verifySNI 会先从
	// 服务端拒掉，判据就测不到客户端"验链不验主机名"的语义了。
	serverCfg := &Config{TLS: TLSConfig{CertPath: certPath, KeyPath: keyPath, VerifyHostName: false}}
	sctx := config.WithConfig(context.Background(), Name, serverCfg)
	cctx := config.WithConfig(context.Background(), Name, clientCfg)

	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost: "127.0.0.1", LocalPort: port,
		RemoteHost: "127.0.0.1", RemotePort: port,
	}
	tctx := config.WithConfig(context.Background(), transport.Name, transportConfig)
	tctx = config.WithConfig(tctx, freedom.Name, &freedom.Config{})
	tcpClient, err := transport.NewClient(tctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tcpServer, err := transport.NewServer(tctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(sctx, tcpServer)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := NewClient(cctx, tcpClient)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// DialConn 内部同步完成握手；有 30s handshakeTimeout 兜底，不会永久挂。
	conn, err := c.DialConn(nil, nil)
	if err != nil {
		return err
	}
	conn.Close()
	time.Sleep(10 * time.Millisecond) // 让 server 的握手 handler 处理完关闭
	return nil
}

// tlsPaths 覆盖客户端两条握手路径：标准库（Fingerprint=""）与 uTLS（chrome 指纹）。
// 两条路各自构造 tls.Config/utls.Config，修复必须都接上，否则带指纹的用户仍然失效。
var tlsPaths = []struct {
	name        string
	fingerprint string
}{
	{name: "stdlib", fingerprint: ""},
	{name: "utls", fingerprint: "chrome"},
}

// verify=true + verify_hostname=false 的语义是"校链但跳主机名"：链可信（根就是服务器
// 证书本身）就应握手成功。修复前 verify_hostname 被忽略、仍走标准校验（链+主机名），
// 主机名不匹配 → 握手失败 → 本用例转红。两条握手路径都要覆盖。
func TestVerifyHostnameFalseSkipsHostnameCheck(t *testing.T) {
	for _, path := range tlsPaths {
		t.Run(path.name, func(t *testing.T) {
			dir := t.TempDir()
			caPath := filepath.Join(dir, "ca.crt")
			if err := os.WriteFile(caPath, []byte(rsa2048Cert), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{TLS: TLSConfig{
				Verify:         true,
				VerifyHostName: false,
				CertPath:       caPath,
				Fingerprint:    path.fingerprint,
				SNI:            "mismatch.example.com", // 与 CN=localhost 不匹配
			}}
			if err := handshakeWithConfig(t, cfg); err != nil {
				t.Fatalf("[%s] verify=true + verify_hostname=false 应跳过主机名校验、握手成功：%v", path.name, err)
			}
		})
	}
}

// 防降级：verify_hostname=false 只能跳主机名，绝不能连链也跳过。
// 客户端信任无关证书（eccCert），服务器证书链不可信，即使主机名也跳过，链校验仍须拒绝。
// 两条握手路径都要覆盖——只要有一条把链也跳了就是安全漏洞。
func TestVerifyHostnameFalseStillRejectsUntrustedChain(t *testing.T) {
	for _, path := range tlsPaths {
		t.Run(path.name, func(t *testing.T) {
			dir := t.TempDir()
			caPath := filepath.Join(dir, "ca.crt")
			if err := os.WriteFile(caPath, []byte(eccCert), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{TLS: TLSConfig{
				Verify:         true,
				VerifyHostName: false,
				CertPath:       caPath, // 无关根，服务器证书链不可信
				Fingerprint:    path.fingerprint,
				SNI:            "mismatch.example.com",
			}}
			if err := handshakeWithConfig(t, cfg); err == nil {
				t.Fatalf("[%s] 链不可信时握手竟成功：verify_hostname=false 把链校验也跳过了，属安全降级", path.name)
			}
		})
	}
}

// verify=false 的原语义不变：链与主机名都不校，即使 SNI 不匹配也握手成功。
func TestVerifyFalseStillSkipsEverything(t *testing.T) {
	cfg := &Config{TLS: TLSConfig{
		Verify:         false,
		VerifyHostName: true, // verify=false 下这一项无意义，不得触发校链
		SNI:            "mismatch.example.com",
	}}
	if err := handshakeWithConfig(t, cfg); err != nil {
		t.Fatalf("verify=false 应保持全关语义、握手成功：%v", err)
	}
}
