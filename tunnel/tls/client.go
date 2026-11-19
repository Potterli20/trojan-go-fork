package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"

	"net"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls/fingerprint"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

var fingerprintsMap = map[string]utls.ClientHelloID{
	"chrome":     utls.HelloChrome_Auto,
	"ios":        utls.HelloIOS_Auto,
	"firefox":    utls.HelloFirefox_Auto,
	"edge":       utls.HelloEdge_Auto,
	"safari":     utls.HelloSafari_Auto,
	"360browser": utls.Hello360_Auto,
	"qqbrowser":  utls.HelloQQ_Auto,
}

// handshakeTimeout 限定 TLS 握手时限,防止对端静默导致 DialConn 永久阻塞
const handshakeTimeout = 30 * time.Second

type Client struct {
	ctx            context.Context
	verify         bool
	verifyHostname bool
	sni            string
	serverName     string
	ca             *x509.CertPool
	cipher         []uint16
	curvePrefs     []tls.CurveID
	utlsCurvePrefs []utls.CurveID
	sessionTicket  bool
	// reuse_session 的客户端实现：两条 TLS 路径各有自己的缓存类型。
	// 每 Client 一份（同一客户端的所有拨号共享）。
	sessionCache     tls.ClientSessionCache
	utlsSessionCache utls.ClientSessionCache
	fingerprint      string
	helloID          utls.ClientHelloID
	keyLogger        io.WriteCloser
	alpn             []string
	underlay         tunnel.Client
	// 幂等闸：proxy/stack.go 明确"重复关闭由各 tunnel 的幂等 Close 兜住"。
	// 库化热重载并发 Close 同一 Client 时，keyLogger（真实为 *os.File）与
	// underlay 会被重复关，第二次 keyLogger.Close 报 file already closed，
	// underlay 的重复 Close 还会级联放大到更下层。
	closeOnce sync.Once
	closeErr  error
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.close() })
	return c.closeErr
}

func (c *Client) close() error {
	log.Info("[TLS] Closing client")
	if c.keyLogger != nil {
		if err := c.keyLogger.Close(); err != nil {
			log.Error("[TLS] Failed to close key logger:", err)
		}
	}
	if err := c.underlay.Close(); err != nil {
		log.Error("[TLS] Failed to close underlay:", err)
		return err
	}
	log.Info("[TLS] Client closed successfully")
	return nil
}

func (c *Client) DialPacket(tunnel tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[TLS] DialPacket is not supported")
	return nil, common.NewError("DialPacket is not supported")
}

// verifyChainOnly 只校验证书链、不校验主机名。
//
// 用途：ssl.verify=true + ssl.verify_hostname=false 时，用户要的是"校验证书链但允许
// IP 连接域名证书"。Go 的 InsecureSkipVerify 会连链带主机名一起关掉，粒度太粗；
// 这里配合 VerifyConnection 回调手工补回链校验——只做 x509.Verify 且不传 DNSName。
//
// 安全不变量：绝不能因为"跳过主机名"就连链也跳过。roots 为 nil 时用系统根池，
// 行为与 InsecureSkipVerify=false 的标准校验的链环节一致。
func verifyChainOnly(certs []*x509.Certificate, roots *x509.CertPool) error {
	if len(certs) == 0 {
		return common.NewError("no peer certificates to verify")
	}
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// 刻意不设 DNSName：这正是 verify_hostname=false 的语义。
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	opts.Intermediates = intermediates
	if _, err := certs[0].Verify(opts); err != nil {
		return common.NewError("certificate chain verification failed").Base(err)
	}
	return nil
}

// chainOnlyVerifyConnection 返回给 tls.Config.VerifyConnection 的回调。
// 两个库（crypto/tls 与 utls）的 ConnectionState 类型不同，但都带 PeerCertificates，
// 所以这里只取证书切片交给共用的 verifyChainOnly。
func (c *Client) chainOnlyVerifyConnection(certs []*x509.Certificate) error {
	return verifyChainOnly(certs, c.ca)
}

func (c *Client) DialConn(address *tunnel.Address, tunnel tunnel.Tunnel) (tunnel.Conn, error) {
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[TLS] DialConn start - target:", address, "sni:", c.sni, "serverName:", c.serverName, "fingerprint:", c.fingerprint, "alpn:", c.alpn)
	}

	if address == nil && c.underlay == nil {
		log.Error("[TLS] Both address and underlay are nil")
		return nil, common.NewError("Address is nil and underlay is nil")
	}

	var conn net.Conn
	var err error
	// address 可能为 nil（如测试直连场景），tunnel.Address.String() 是指针方法，需判空
	var target string
	if address != nil {
		target = address.String()
	}
	tracker := log.NewConnectionTracker("TLS", "DialConn").
		WithField("target", target).
		WithField("sni", c.sni).
		WithField("fingerprint", c.fingerprint)

	if c.underlay != nil {
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] Dialing with underlay tunnel...")
		}
		tConn, err := c.underlay.DialConn(address, &Tunnel{})
		if err != nil {
			_ = tracker.Error(err)
			return nil, common.NewError("failed to dial with underlay tunnel").Base(err)
		}
		conn = tConn
	} else {
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] Dialing TCP directly to:", address.String())
		}
		// 带超时的拨号：无超时的 net.Dial 挂起时既无法取消也可能拖住调用方约 2 分钟
		dialer := net.Dialer{Timeout: 30 * time.Second}
		conn, err = dialer.Dial("tcp", address.String())
		if err != nil {
			_ = tracker.Error(err)
			return nil, common.NewError("failed to dial TCP connection").Base(err)
		}
	}

	tlsServerName := c.serverName
	if tlsServerName == "" {
		tlsServerName = c.sni
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] ServerName is empty, using SNI:", tlsServerName)
		}
	}
	log.Info("[TLS] Starting handshake with ServerName:", tlsServerName)

	// ssl.verify / ssl.verify_hostname 的组合：
	//   verify=false                    → 全关（原语义，保持兼容）
	//   verify=true  + hostname=true    → 标准校验（链 + 主机名）
	//   verify=true  + hostname=false   → 只跳主机名，用 VerifyConnection 手工补回链校验
	// 不能只用 !verify 设 InsecureSkipVerify：那会把"只跳主机名"误变成"链也不校"的安全降级。
	skipVerify := !c.verify || !c.verifyHostname
	chainOnly := c.verify && !c.verifyHostname

	if c.fingerprint != "" {
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] Using uTLS fingerprint:", c.helloID)
		}
		utlsConfig := &utls.Config{
			RootCAs:                c.ca,
			ServerName:             tlsServerName,
			InsecureSkipVerify:     skipVerify,
			KeyLogWriter:           c.keyLogger,
			CipherSuites:           c.cipher,
			SessionTicketsDisabled: !c.sessionTicket,
			ClientSessionCache:     c.utlsSessionCache,
			NextProtos:             c.alpn,
			CurvePreferences:       c.utlsCurvePrefs,
		}
		if chainOnly {
			// InsecureSkipVerify 下标准库/utls 都不会再校链，VerifyConnection 仍会调用且
			// PeerCertificates 已填充——在这里手工补回链校验（不传 DNSName = 跳主机名）。
			utlsConfig.VerifyConnection = func(cs utls.ConnectionState) error {
				return c.chainOnlyVerifyConnection(cs.PeerCertificates)
			}
		}
		uconn := utls.UClient(conn, utlsConfig, c.helloID)

		conn.SetDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		err := uconn.HandshakeContext(c.handshakeContext())
		conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		if err != nil {
			log.Error("[TLS] uTLS handshake failed:", err)
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			return nil, common.NewError("TLS handshake failed").Base(err)
		}
		_ = tracker.Success()
		if log.ShouldLog(log.DebugLevel) {
			state := uconn.ConnectionState()
			log.Debug("[TLS] Negotiated protocol:", state.NegotiatedProtocol)
			log.Debug("[TLS] TLS Version:", state.Version)
			log.Debug("[TLS] Cipher Suite:", state.CipherSuite)
		}

		return &transport.Conn{Conn: uconn}, nil
	}

	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[TLS] Using standard Go TLS library")
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify:     skipVerify,
		ServerName:             tlsServerName,
		RootCAs:                c.ca,
		KeyLogWriter:           c.keyLogger,
		CipherSuites:           c.cipher,
		SessionTicketsDisabled: !c.sessionTicket,
		ClientSessionCache:     c.sessionCache,
		NextProtos:             c.alpn,
		CurvePreferences:       c.curvePrefs,
	}
	if chainOnly {
		tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			return c.chainOnlyVerifyConnection(cs.PeerCertificates)
		}
	}
	tlsConn := tls.Client(conn, tlsConfig)

	conn.SetDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	err = tlsConn.HandshakeContext(c.handshakeContext())
	conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	if err != nil {
		log.Error("[TLS] Handshake failed:", err)
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("TLS handshake failed").Base(err)
	}
	_ = tracker.Success()
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[TLS] Negotiated protocol:", tlsConn.ConnectionState().NegotiatedProtocol)
		log.Debug("[TLS] TLS Version:", tlsConn.ConnectionState().Version)
		log.Debug("[TLS] Cipher Suite:", tlsConn.ConnectionState().CipherSuite)
	}

	return &transport.Conn{Conn: tlsConn}, nil
}

// handshakeContext 返回握手要绑的 ctx。手工构造的 Client（测试）可能没有 ctx，
// 用 Background 兜底而不是 panic。
func (c *Client) handshakeContext() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	// 曲线偏好必须在建栈期校验：两个 Parse* 对认不出的名字过去直接 continue，拼错的
	// "X25519MLKEM768"（少个 8）或只有一侧认的名字会被无声丢掉——用户以为密钥交换换成了
	// 后量子，实际用的是 Go 默认集合，而握手照样成功、毫无症状。
	if err := fingerprint.ValidateCurvePreferences(cfg.TLS.CurvePreferences); err != nil {
		return nil, err
	}
	// cipher 名字同样必须在建栈期校验：ParseCipher 对认不出的名字静默跳过，
	// 全配错时 CipherSuites 拿到 nil 走 Go 默认全套，"限制加密套件"的意图被架空。
	if err := fingerprint.ValidateCipher(strings.Split(cfg.TLS.Cipher, ":")); err != nil {
		return nil, err
	}

	log.Info("[TLS] Creating client")
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[TLS] RemoteHost:", cfg.RemoteHost)
		log.Debug("[TLS] RemotePort:", cfg.RemotePort)
		log.Debug("[TLS] TLS SNI:", cfg.TLS.SNI)
		log.Debug("[TLS] TLS ServerName:", cfg.TLS.ServerName)
		log.Debug("[TLS] TLS Fingerprint:", cfg.TLS.Fingerprint)
		log.Debug("[TLS] TLS ALPN:", cfg.TLS.ALPN)
		log.Debug("[TLS] TLS Verify:", cfg.TLS.Verify)
		log.Debug("[TLS] TLS ReuseSession:", cfg.TLS.ReuseSession)
		log.Debug("[TLS] TLS Cipher:", cfg.TLS.Cipher)
	}

	helloID, err := getHelloID(cfg.TLS.Fingerprint)
	if err != nil {
		log.Error("[TLS] Failed to get HelloID:", err)
		return nil, err
	}

	if cfg.TLS.SNI == "" {
		cfg.TLS.SNI = cfg.RemoteHost
		log.Warn("[TLS] TLS SNI is unspecified, using remote_addr:", cfg.TLS.SNI)
	}

	if cfg.TLS.ServerName == "" {
		cfg.TLS.ServerName = cfg.TLS.SNI
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] TLS ServerName is unspecified, using SNI:", cfg.TLS.ServerName)
		}
	}

	var keyLogger io.WriteCloser
	// keyLogger 在本函数内打开，但唯一的归还入口是 Client.Close()。本函数此后还有
	// 错误返回（证书文件读不到等），那些路径上 client 对象不会被交出去，句柄就永久
	// 漏掉——实测 20 次失败留下 20 个 fd。用一个 success 标志在返回前兜住所有失败路径。
	keyLoggerOwned := false
	defer func() {
		if !keyLoggerOwned && keyLogger != nil {
			// 可写句柄的失败往往只在 Close 时才暴露（缓冲未落盘就丢数据），
			// 所以这里的错误不能丢——CodeQL go/unhandled-writable-file-close 正是指它。
			if err := keyLogger.Close(); err != nil {
				log.Warn("[TLS] Failed to close key log file:", err)
			}
		}
	}()
	if cfg.TLS.KeyLogPath != "" {
		keyLogger, err = os.OpenFile(cfg.TLS.KeyLogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			log.Warn("[TLS] Failed to open key log file:", err)
		} else {
			log.Info("[TLS] TLS key logging enabled to:", cfg.TLS.KeyLogPath)
		}
	}

	client := &Client{
		ctx:            ctx,
		underlay:       underlay,
		verify:         cfg.TLS.Verify,
		verifyHostname: cfg.TLS.VerifyHostName,
		sni:            cfg.TLS.SNI,
		serverName:     cfg.TLS.ServerName,
		cipher:         fingerprint.ParseCipher(strings.Split(cfg.TLS.Cipher, ":")),
		curvePrefs:     fingerprint.ParseCurvePreferences(cfg.TLS.CurvePreferences),
		utlsCurvePrefs: fingerprint.ParseUTLSCurvePreferences(cfg.TLS.CurvePreferences),
		sessionTicket:  cfg.TLS.ReuseSession,
		fingerprint:    cfg.TLS.Fingerprint,
		helloID:        helloID,
		keyLogger:      keyLogger,
		alpn:           cfg.TLS.ALPN,
	}
	if client.sessionTicket {
		// 每 Client 一份、容量 64：客户端侧的会话缓存是 reuse_session 唯一真正
		// 起作用的组件（SessionTicketsDisabled 是服务端字段，客户端摆着没用）。
		client.sessionCache = tls.NewLRUClientSessionCache(64)
		client.utlsSessionCache = utls.NewLRUClientSessionCache(64)
	}

	if cfg.TLS.CertPath != "" {
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] Loading custom certificate from:", cfg.TLS.CertPath)
		}
		if err := loadCert(client, cfg.TLS.CertPath); err != nil {
			log.Error("[TLS] Failed to load certificate:", err)
			return nil, err
		}
	} else {
		log.Info("[TLS] Using default CA list for certificate verification")
	}

	keyLoggerOwned = true // 从这里开始由 Client.Close() 负责归还
	log.Info("[TLS] Client created successfully")
	return client, nil
}

func getHelloID(fingerprint string) (utls.ClientHelloID, error) {
	if fingerprint == "" {
		log.Info("[TLS] No fingerprint specified, using Chrome as default")
		return utls.HelloChrome_Auto, nil
	}

	helloID, ok := fingerprintsMap[strings.ToLower(fingerprint)]
	if !ok {
		errMsg := fmt.Sprintf("Invalid fingerprint '%s'. Valid values: chrome, ios, firefox, edge, safari, 360browser, qqbrowser", fingerprint)
		log.Error("[TLS]", errMsg)
		return utls.ClientHelloID{}, common.NewError(errMsg)
	}

	log.Info("[TLS] Using TLS fingerprint:", fingerprint)
	return helloID, nil
}

func loadCert(client *Client, certPath string) error {
	caCertByte, err := os.ReadFile(certPath) //gosec:disable -- 路径来自配置文件，由用户自己控制，非外部输入
	if err != nil {
		return common.NewError("failed to load cert file at path: " + certPath).Base(err)
	}
	client.ca = x509.NewCertPool()
	if ok := client.ca.AppendCertsFromPEM(caCertByte); !ok {
		log.Warn("[TLS] Failed to append certificates from", certPath)
	}
	log.Info("[TLS] Custom certificate loaded successfully")

	pemCerts := caCertByte
	certCount := 0
	for len(pemCerts) > 0 {
		var block *pem.Block
		block, pemCerts = pem.Decode(pemCerts)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		certCount++
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[TLS] Certificate #", certCount, "- Issuer:", cert.Issuer, "Subject:", cert.Subject)
		}
	}
	log.Info("[TLS] Total certificates loaded:", certCount)

	return nil
}
