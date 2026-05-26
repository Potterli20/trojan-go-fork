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

type Client struct {
	verify        bool
	sni           string
	serverName    string
	ca            *x509.CertPool
	cipher        []uint16
	sessionTicket bool
	reuseSession  bool
	fingerprint   string
	helloID       utls.ClientHelloID
	keyLogger     io.WriteCloser
	alpn          []string
	underlay      tunnel.Client
}

func (c *Client) Close() error {
	log.Info("[TLS Client] Closing TLS client connection")
	if c.keyLogger != nil {
		if err := c.keyLogger.Close(); err != nil {
			log.Error("[TLS Client] Failed to close key logger:", err)
		}
	}
	if err := c.underlay.Close(); err != nil {
		log.Error("[TLS Client] Failed to close underlay:", err)
		return err
	}
	log.Info("[TLS Client] TLS client closed successfully")
	return nil
}

func (c *Client) DialPacket(tunnel tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[TLS Client] DialPacket is not supported")
	return nil, common.NewError("DialPacket is not supported")
}

func (c *Client) DialConn(address *tunnel.Address, tunnel tunnel.Tunnel) (tunnel.Conn, error) {
	log.Debug("[TLS Client] ========== TLS DialConn Start ==========")
	log.Debug("[TLS Client] Target address:", address)
	log.Debug("[TLS Client] TLS SNI:", c.sni)
	log.Debug("[TLS Client] TLS ServerName:", c.serverName)
	log.Debug("[TLS Client] TLS Fingerprint:", c.fingerprint)
	log.Debug("[TLS Client] TLS ALPN:", c.alpn)

	if address == nil && c.underlay == nil {
		log.Error("[TLS Client] Both address and underlay are nil")
		return nil, common.NewError("Address is nil and underlay is nil")
	}

	var conn net.Conn
	var err error

	if c.underlay != nil {
		log.Debug("[TLS Client] Dialing with underlay tunnel...")
		startTime := time.Now()
		tConn, err := c.underlay.DialConn(address, &Tunnel{})
		dialDuration := time.Since(startTime)
		if err != nil {
			log.Error("[TLS Client] Failed to dial with underlay tunnel after", dialDuration, ":", err)
			return nil, common.NewError("failed to dial with underlay tunnel").Base(err)
		}
		conn = tConn
		log.Info("[TLS Client] Underlay connection established in", dialDuration)
	} else {
		log.Debug("[TLS Client] Dialing TCP directly to:", address.String())
		startTime := time.Now()
		conn, err = net.Dial("tcp", address.String())
		dialDuration := time.Since(startTime)
		if err != nil {
			log.Error("[TLS Client] Failed to dial TCP connection after", dialDuration, ":", err)
			return nil, common.NewError("failed to dial TCP connection").Base(err)
		}
		log.Info("[TLS Client] TCP connection established in", dialDuration, "to", address.String())
	}

	tlsServerName := c.serverName
	if tlsServerName == "" {
		tlsServerName = c.sni
		log.Debug("[TLS Client] ServerName is empty, using SNI:", tlsServerName)
	}
	log.Info("[TLS Client] Starting TLS handshake with ServerName:", tlsServerName)

	if c.fingerprint != "" {
		log.Debug("[TLS Client] Using uTLS fingerprint:", c.helloID)
		uconn := utls.UClient(conn, &utls.Config{
			RootCAs:                c.ca,
			ServerName:             tlsServerName,
			InsecureSkipVerify:     !c.verify,
			KeyLogWriter:           c.keyLogger,
			CipherSuites:           c.cipher,
			SessionTicketsDisabled: !c.sessionTicket,
			NextProtos:             c.alpn,
		}, c.helloID)

		startTime := time.Now()
		if err := uconn.Handshake(); err != nil {
			log.Error("[TLS Client] uTLS handshake failed after", time.Since(startTime), ":", err)
			conn.Close()
			return nil, common.NewError("TLS handshake failed").Base(err)
		}
		handshakeDuration := time.Since(startTime)
		log.Info("[TLS Client] uTLS handshake succeeded in", handshakeDuration)
		state := uconn.ConnectionState()
		log.Debug("[TLS Client] Negotiated protocol:", state.NegotiatedProtocol)
		log.Debug("[TLS Client] TLS Version:", state.Version)
		log.Debug("[TLS Client] Cipher Suite:", state.CipherSuite)

		return &transport.Conn{Conn: uconn}, nil
	}

	log.Debug("[TLS Client] Using standard Go TLS library")
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify:     !c.verify,
		ServerName:             tlsServerName,
		RootCAs:                c.ca,
		KeyLogWriter:           c.keyLogger,
		CipherSuites:           c.cipher,
		SessionTicketsDisabled: !c.sessionTicket,
		NextProtos:             c.alpn,
	})

	startTime := time.Now()
	if err := tlsConn.Handshake(); err != nil {
		log.Error("[TLS Client] TLS handshake failed after", time.Since(startTime), ":", err)
		conn.Close()
		return nil, common.NewError("TLS handshake failed").Base(err)
	}
	handshakeDuration := time.Since(startTime)
	log.Info("[TLS Client] TLS handshake succeeded in", handshakeDuration)
	log.Debug("[TLS Client] Negotiated protocol:", tlsConn.ConnectionState().NegotiatedProtocol)
	log.Debug("[TLS Client] TLS Version:", tlsConn.ConnectionState().Version)
	log.Debug("[TLS Client] Cipher Suite:", tlsConn.ConnectionState().CipherSuite)

	log.Debug("[TLS Client] ========== TLS DialConn End ==========")
	return &transport.Conn{Conn: tlsConn}, nil
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	log.Info("[TLS Client] ========== Creating TLS Client ==========")
	log.Debug("[TLS Client] RemoteHost:", cfg.RemoteHost)
	log.Debug("[TLS Client] RemotePort:", cfg.RemotePort)
	log.Debug("[TLS Client] TLS SNI:", cfg.TLS.SNI)
	log.Debug("[TLS Client] TLS ServerName:", cfg.TLS.ServerName)
	log.Debug("[TLS Client] TLS Fingerprint:", cfg.TLS.Fingerprint)
	log.Debug("[TLS Client] TLS ALPN:", cfg.TLS.ALPN)
	log.Debug("[TLS Client] TLS Verify:", cfg.TLS.Verify)
	log.Debug("[TLS Client] TLS ReuseSession:", cfg.TLS.ReuseSession)
	log.Debug("[TLS Client] TLS Cipher:", cfg.TLS.Cipher)

	helloID, err := getHelloID(cfg.TLS.Fingerprint)
	if err != nil {
		log.Error("[TLS Client] Failed to get HelloID:", err)
		return nil, err
	}

	if cfg.TLS.SNI == "" {
		cfg.TLS.SNI = cfg.RemoteHost
		log.Warn("[TLS Client] TLS SNI is unspecified, using remote_addr as default:", cfg.TLS.SNI)
	}

	if cfg.TLS.ServerName == "" {
		cfg.TLS.ServerName = cfg.TLS.SNI
		log.Debug("[TLS Client] TLS ServerName is unspecified, using SNI as default:", cfg.TLS.ServerName)
	}

	var keyLogger io.WriteCloser
	if cfg.TLS.KeyLogPath != "" {
		keyLogger, err = os.OpenFile(cfg.TLS.KeyLogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			log.Warn("[TLS Client] Failed to open key log file:", err)
		} else {
			log.Info("[TLS Client] TLS key logging enabled to:", cfg.TLS.KeyLogPath)
		}
	}

	client := &Client{
		underlay:      underlay,
		verify:        cfg.TLS.Verify,
		sni:           cfg.TLS.SNI,
		serverName:    cfg.TLS.ServerName,
		cipher:        fingerprint.ParseCipher(strings.Split(cfg.TLS.Cipher, ":")),
		sessionTicket: cfg.TLS.ReuseSession,
		fingerprint:   cfg.TLS.Fingerprint,
		helloID:       helloID,
		keyLogger:     keyLogger,
		alpn:          cfg.TLS.ALPN,
	}

	if cfg.TLS.CertPath != "" {
		log.Debug("[TLS Client] Loading custom certificate from:", cfg.TLS.CertPath)
		if err := loadCert(client, cfg.TLS.CertPath); err != nil {
			log.Error("[TLS Client] Failed to load certificate:", err)
			return nil, err
		}
	} else {
		log.Info("[TLS Client] Using default CA list for certificate verification")
	}

	log.Info("[TLS Client] ========== TLS Client Created Successfully ==========")
	return client, nil
}

func getHelloID(fingerprint string) (utls.ClientHelloID, error) {
	if fingerprint == "" {
		log.Info("[TLS Client] No fingerprint specified, using Chrome as default")
		return utls.HelloChrome_Auto, nil
	}

	helloID, ok := fingerprintsMap[strings.ToLower(fingerprint)]
	if !ok {
		errMsg := fmt.Sprintf("Invalid fingerprint '%s'. Valid values: chrome, ios, firefox, edge, safari, 360browser, qqbrowser", fingerprint)
		log.Error("[TLS Client]", errMsg)
		return utls.ClientHelloID{}, common.NewError(errMsg)
	}

	log.Info("[TLS Client] Using TLS fingerprint:", fingerprint)
	return helloID, nil
}

func loadCert(client *Client, certPath string) error {
	caCertByte, err := os.ReadFile(certPath)
	if err != nil {
		return common.NewError("failed to load cert file at path: " + certPath).Base(err)
	}
	client.ca = x509.NewCertPool()
	if ok := client.ca.AppendCertsFromPEM(caCertByte); !ok {
		log.Warn("[TLS Client] Failed to append certificates from", certPath)
	}
	log.Info("[TLS Client] Custom certificate loaded successfully")

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
		log.Debug("[TLS Client] Certificate #", certCount, "- Issuer:", cert.Issuer, "Subject:", cert.Subject)
	}
	log.Info("[TLS Client] Total certificates loaded:", certCount)

	return nil
}
