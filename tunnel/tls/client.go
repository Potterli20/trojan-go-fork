package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"
	"net"
	"strings"

	utls "github.com/sagernet/utls"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls/fingerprint"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// Client is a tls client
type Client struct {
	verify        bool
	sni           string
	ca            *x509.CertPool
	cipher        []uint16
	sessionTicket bool
	reuseSession  bool
	fingerprint   string
	helloID       utls.ClientHelloID
	keyLogger     io.WriteCloser
	underlay      tunnel.Client
}

func (c *Client) Close() error {
	if c.keyLogger != nil {
		c.keyLogger.Close()
	}
	return c.underlay.Close()
}

func (c *Client) DialPacket(tunnel tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("DialPacket is not supported")
}

func (c *Client) DialConn(address *tunnel.Address, tunnel tunnel.Tunnel) (tunnel.Conn, error) {
	if address == nil {
		return nil, common.NewError("Address is nil")
	}

	conn, err := net.Dial("tcp", address.String())
	if err != nil {
		return nil, common.NewError("failed to dial TCP connection").Base(err)
	}

	var tlsConn *tls.Conn
	if c.fingerprint != "" {
		// Use utls for fingerprinting
		tlsConn = utls.UClient(conn, &utls.Config{
			RootCAs:            c.ca,
			ServerName:         c.sni,
			InsecureSkipVerify: !c.verify,
			KeyLogWriter:       c.keyLogger,
		}, c.helloID).Conn
	} else {
		// Use default Go TLS library
		tlsConn = tls.Client(conn, &tls.Config{
			InsecureSkipVerify:     !c.verify,
			ServerName:             c.sni,
			RootCAs:                c.ca,
			KeyLogWriter:           c.keyLogger,
			CipherSuites:           c.cipher,
			SessionTicketsDisabled: !c.sessionTicket,
		})
	}

	if err := tlsConn.Handshake(); err != nil {
		return nil, common.NewError("TLS handshake failed").Base(err)
	}

	return &transport.Conn{Conn: tlsConn}, nil
}

// NewClient creates a tls client
func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	helloID, err := getHelloID(cfg.TLS.Fingerprint)
	if err != nil {
		return nil, err
	}

	if cfg.TLS.SNI == "" {
		cfg.TLS.SNI = cfg.RemoteHost
		log.Warn("TLS SNI is unspecified, it's recommended to specify it for better security")
	}

	client := &Client{
		underlay:      underlay,
		verify:        cfg.TLS.Verify,
		sni:           cfg.TLS.SNI,
		cipher:        fingerprint.ParseCipher(strings.Split(cfg.TLS.Cipher, ":")),
		sessionTicket: cfg.TLS.ReuseSession,
		fingerprint:   cfg.TLS.Fingerprint,
		helloID:       helloID,
	}

	if cfg.TLS.CertPath != "" {
		if err := loadCert(client, cfg.TLS.CertPath); err != nil {
			return nil, err
		}
	} else {
		log.Info("Cert is unspecified, using default CA list")
	}

	log.Debug("TLS client created")
	return client, nil
}

func getHelloID(fingerprint string) (utls.ClientHelloID, error) {
	fingerprints := map[string]utls.ClientHelloID{
		"chrome":     utls.HelloChrome_Auto,
		"ios":        utls.HelloIOS_Auto,
		"firefox":    utls.HelloFirefox_Auto,
		"edge":       utls.HelloEdge_Auto,
		"safari":     utls.HelloSafari_Auto,
		"360browser": utls.Hello360_Auto,
		"qqbrowser":  utls.HelloQQ_Auto,
	}

	if fingerprint == "" {
		log.Info("No 'fingerprint' value specified in your configuration. Your trojan's TLS fingerprint will look like Chrome by default.")
		return utls.HelloChrome_Auto, nil
	}

	helloID, ok := fingerprints[strings.ToLower(fingerprint)]
	if !ok {
		return utls.ClientHelloID{}, common.NewError("Invalid 'fingerprint' value in configuration: '" + fingerprint + "'. Possible values are 'chrome' (default), 'ios', 'firefox', 'edge', 'safari', '360browser', or 'qqbrowser'.")
	}

	log.Info("Your trojan's TLS fingerprint will look like", fingerprint)
	return helloID, nil
}

func loadCert(client *Client, certPath string) error {
	caCertByte, err := ioutil.ReadFile(certPath)
	if err != nil {
		return common.NewError("failed to load cert file at path: " + certPath).Base(err)
	}
	client.ca = x509.NewCertPool()
	if ok := client.ca.AppendCertsFromPEM(caCertByte); !ok {
		log.Warn("Invalid cert list")
	}
	log.Info("Using custom cert")

	// Print cert info
	pemCerts := caCertByte
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
		log.Trace("Issuer:", cert.Issuer, "Subject:", cert.Subject)
	}

	return nil
}
