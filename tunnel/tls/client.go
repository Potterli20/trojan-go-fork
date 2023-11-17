package tls

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"
	"strings"

	tls "github.com/refraction-networking/utls"

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
	helloID       tls.ClientHelloID
	keyLogger     io.WriteCloser
	underlay      tunnel.Client
}

// NewClient creates a new TLS client.
func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	helloID, err := getHelloID(cfg.TLS.Fingerprint)
	if err != nil {
		return nil, err
	}

	if cfg.TLS.SNI == "" {
		cfg.TLS.SNI = cfg.RemoteHost
		log.Warn("tls sni is unspecified, it's recommended to specify it for better security")
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
		err := loadCert(client, cfg.TLS.CertPath)
		if err != nil {
			return nil, err
		}
	} else {
		log.Info("cert is unspecified, using default ca list")
	}

	log.Debug("tls client created")
	return client, nil
}

// getHelloID returns the TLS client hello ID based on the given fingerprint.
func getHelloID(fingerprint string) (tls.ClientHelloID, error) {
	fingerprints := map[string]tls.ClientHelloID{
		"chrome":     tls.HelloChrome_Auto,
		"ios":        tls.HelloIOS_Auto,
		"firefox":    tls.HelloFirefox_Auto,
		"edge":       tls.HelloEdge_Auto,
		"safari":     tls.HelloSafari_Auto,
		"360browser": tls.Hello360_Auto,
		"qqbrowser":  tls.HelloQQ_Auto,
	}

	if fingerprint == "" {
		log.Info("No 'fingerprint' value specified in your configuration. Your trojan's TLS fingerprint will look like Chrome by default.")
		return tls.HelloChrome_Auto, nil
	}

	helloID, ok := fingerprints[strings.ToLower(fingerprint)]
	if !ok {
		return tls.ClientHelloID{}, common.NewError("Invalid 'fingerprint' value in configuration: '" + fingerprint + "'. Possible values are 'chrome' (default), 'ios', 'firefox', 'edge', 'safari', '360browser', or 'qqbrowser'.")
	}

	log.Info("Your trojan's TLS fingerprint will look like", fingerprint)
	return helloID, nil
}

// loadCert loads the TLS CA certificate from the given certificate path.
func loadCert(client *Client, certPath string) error {
	caCertByte, err := ioutil.ReadFile(certPath)
	if err != nil {
		return common.NewError("failed to load cert file at path: " + certPath).Base(err)
	}
	client.ca = x509.NewCertPool()
	ok := client.ca.AppendCertsFromPEM(caCertByte)
	if !ok {
		log.Warn("invalid cert list")
	}
	log.Info("using custom cert")

	// print cert info
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
		log.Trace("issuer:", cert.Issuer, "subject:", cert.Subject)
	}

	return nil
}
