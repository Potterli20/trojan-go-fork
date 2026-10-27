package mysql

// 连接参数构造的判据。这一组用例钉的是 statistic/mysql 里最容易"静默失效"的一段：
// DSN 拼装（凭据字符、IPv6）与 TLS 配置（全局注册表覆盖）。

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestMySQLConfigFromKeepsCredentialsOutOfTheParameterSection(t *testing.T) {
	// 旧实现手拼 `user:pass@tcp(host:port)/db?charset=utf8`。驱动按**第一个 '?'** 切分参数段，
	// 于是口令里的 '?' 会把 "@tcp(...)" 整段变成参数区：连接目标被改写，甚至可以注入
	// tls=、allowallfiles= 这类驱动参数。
	const (
		user = "ro?user"
		pass = "p@ss?wo&rd/1:value"
	)
	cfg, err := mysqlConfigFrom(user, pass, "db.example.com", 3307, "trojan", "", "", "")
	if err != nil {
		t.Fatalf("mysqlConfigFrom: %v", err)
	}
	if cfg.User != user || cfg.Passwd != pass {
		t.Fatalf("凭据被改写了：user=%q pass=%q", cfg.User, cfg.Passwd)
	}
	if cfg.Addr != "db.example.com:3307" || cfg.Net != "tcp" || cfg.DBName != "trojan" {
		t.Fatalf("连接目标不对：net=%q addr=%q db=%q", cfg.Net, cfg.Addr, cfg.DBName)
	}

	// 走一遍 FormatDSN → ParseDSN：必须逐字还原，且参数段里只有 charset 这一项。
	dsn := cfg.FormatDSN()
	back, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("我们自己生成的 DSN 解析失败：%v（dsn=%s）", err, dsn)
	}
	if back.User != user || back.Passwd != pass || back.Addr != cfg.Addr || back.DBName != cfg.DBName {
		t.Fatalf("DSN 往返后连接参数被改写：user=%q pass=%q addr=%q db=%q（dsn=%s）",
			back.User, back.Passwd, back.Addr, back.DBName, dsn)
	}
	for k := range back.Params {
		if k != "charset" {
			t.Fatalf("参数段里多出一项 %q，等于允许凭据注入驱动参数（dsn=%s）", k, dsn)
		}
	}
}

func TestMySQLConfigFromFormatsIPv6Host(t *testing.T) {
	// 裸拼接会把 IPv6 写成 tcp(::1:3306)，地址解析直接错乱。
	cfg, err := mysqlConfigFrom("u", "p", "::1", 3306, "trojan", "", "", "")
	if err != nil {
		t.Fatalf("mysqlConfigFrom: %v", err)
	}
	if want := net.JoinHostPort("::1", "3306"); cfg.Addr != want {
		t.Fatalf("IPv6 主机的 Addr 是 %q，期望 %q", cfg.Addr, want)
	}
}

func TestMySQLConfigFromBoundsEveryPhaseOfIO(t *testing.T) {
	// 没有超时的 DSN 是"到期判定整体停摆"的入口：syncUsers 的 ctx 只在 Close 才取消，
	// 一次半开连接就永久卡住那个单协程 updater，用户增减/quota/expire/流量写回全部静默。
	cfg, err := mysqlConfigFrom("u", "p", "h", 3306, "d", "", "", "")
	if err != nil {
		t.Fatalf("mysqlConfigFrom: %v", err)
	}
	if cfg.Timeout != mysqlDialTimeout || cfg.ReadTimeout != mysqlIOTimeout || cfg.WriteTimeout != mysqlIOTimeout {
		t.Fatalf("缺少有界时限：dial=%v read=%v write=%v", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
	if cfg.Timeout <= 0 || cfg.ReadTimeout <= 0 || cfg.WriteTimeout <= 0 {
		t.Fatalf("三段时限都必须为正：dial=%v read=%v write=%v", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
}

func TestMySQLConfigFromTLSIsCarriedByTheConnectorNotTheGlobalRegistry(t *testing.T) {
	caPath := writeCATestFile(t)
	cfg, err := mysqlConfigFrom("u", "p", "h", 3306, "d", "", "", caPath)
	if err != nil {
		t.Fatalf("mysqlConfigFrom: %v", err)
	}
	// mysql.RegisterTLSConfig 写的是驱动包里**按名字覆盖**的全局表：两个认证器（配置热重建
	// 时新旧实例短期并存；custom 模式多入站同理）用不同 ca 时，后者会悄悄改掉前者的握手材料。
	// 现在 TLS 材料挂在连接器自己身上。
	if cfg.TLSConfig != "" {
		t.Fatalf("仍在用全局 TLS 配置名 %q：并发实例会互相覆盖", cfg.TLSConfig)
	}
	if cfg.TLS == nil {
		t.Fatal("ca 非空时必须有 TLS 配置")
	}
	if cfg.TLS.MinVersion != tls.VersionTLS12 {
		t.Fatalf("最低版本必须是 TLS 1.2，实际 %x", cfg.TLS.MinVersion)
	}
	if cfg.TLS.RootCAs == nil {
		t.Fatal("RootCAs 没有装上提供的 CA")
	}
	if len(cfg.TLS.Certificates) != 0 {
		t.Fatal("没给客户端证书却带上了证书")
	}

	// 没有 ca ⇒ 完全不启用 TLS（与改造前一致）。
	plain, err := mysqlConfigFrom("u", "p", "h", 3306, "d", "", "", "")
	if err != nil {
		t.Fatalf("mysqlConfigFrom: %v", err)
	}
	if plain.TLS != nil {
		t.Fatal("ca 为空时不应该启用 TLS")
	}
}

func TestMySQLConfigFromClientCertPair(t *testing.T) {
	caPath, certPath, keyPath := writeClientCertTestFiles(t)
	cfg, err := mysqlConfigFrom("u", "p", "h", 3306, "d", keyPath, certPath, caPath)
	if err != nil {
		t.Fatalf("成对的 cert/key 应当被接受：%v", err)
	}
	if len(cfg.TLS.Certificates) != 1 {
		t.Fatalf("客户端证书数量是 %d，期望 1", len(cfg.TLS.Certificates))
	}

	// 只给一个 ⇒ 配置错误，文案必须与文档一致。
	if _, err := mysqlConfigFrom("u", "p", "h", 3306, "d", keyPath, "", caPath); err == nil ||
		!strings.Contains(err.Error(), "set both key and cert, or set neither") {
		t.Fatalf("只给 key 时必须报既有文案，实际：%v", err)
	}
}

func TestMySQLConfigFromMissingCAFileFails(t *testing.T) {
	nope := filepath.Join(t.TempDir(), "absent.pem")
	if _, err := mysqlConfigFrom("u", "p", "h", 3306, "d", "", "", nope); err == nil {
		t.Fatal("ca 文件读不到时必须返回错误")
	}
}

// --- 测试夹具：现场签一张 CA 与一张客户端证书，避免依赖仓库里的证书文件 ---

func writeCATestFile(t *testing.T) string {
	t.Helper()
	der, _, _ := mintTestCert(t, true)
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("写 ca: %v", err)
	}
	return path
}

func writeClientCertTestFiles(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()
	caDER, caKey, serial := mintTestCert(t, true)
	caPath = filepath.Join(t.TempDir(), "ca.pem")
	certPath = filepath.Join(t.TempDir(), "client.pem")
	keyPath = filepath.Join(t.TempDir(), "client.key")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Int64() + 1),
		Subject:      pkix.Name{CommonName: "trojan-go-mysql-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成客户端密钥: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, mustParseCA(t, caDER, caKey), &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("签发客户端证书: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("编码客户端密钥: %v", err)
	}
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)
	return
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
}

func mintTestCert(t *testing.T, isCA bool) (der []byte, key *ecdsa.PrivateKey, serial *big.Int) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥: %v", err)
	}
	serial = big.NewInt(0x726f6a616e)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "trojan-go-mysql-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("自签证书: %v", err)
	}
	return
}

func mustParseCA(t *testing.T, der []byte, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析 CA 证书: %v", err)
	}
	return cert
}
