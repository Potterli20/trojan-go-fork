package tls

import (
	"github.com/Potterli20/trojan-go-fork/config"
)

type Config struct {
	RemoteHost string          `json:"remote_addr" yaml:"remote-addr"`
	RemotePort int             `json:"remote_port" yaml:"remote-port"`
	TLS        TLSConfig       `json:"ssl" yaml:"ssl"`
	Websocket  WebsocketConfig `json:"websocket" yaml:"websocket"`
}

type WebsocketConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type TLSConfig struct {
	Verify               bool     `json:"verify" yaml:"verify"`
	VerifyHostName       bool     `json:"verify_hostname" yaml:"verify-hostname"`
	CertPath             string   `json:"cert" yaml:"cert"`
	KeyPath              string   `json:"key" yaml:"key"`
	KeyPassword          string   `json:"key_password" yaml:"key-password"`
	Cipher               string   `json:"cipher" yaml:"cipher"`
	SNI                  string   `json:"sni" yaml:"sni"`
	ServerName           string   `json:"server_name" yaml:"server-name"`
	HTTPResponseFileName string   `json:"plain_http_response" yaml:"plain-http-response"`
	FallbackHost         string   `json:"fallback_addr" yaml:"fallback-addr"`
	FallbackPort         int      `json:"fallback_port" yaml:"fallback-port"`
	ReuseSession         bool     `json:"reuse_session" yaml:"reuse-session"`
	ALPN                 []string `json:"alpn" yaml:"alpn"`
	Fingerprint          string   `json:"fingerprint" yaml:"fingerprint"`
	KeyLogPath           string   `json:"key_log" yaml:"key-log"`
	CertCheckRate        int      `json:"cert_check_rate" yaml:"cert-check-rate"`
	// CurvePreferences 指定 TLS 密钥交换机制偏好列表。
	// 支持的值：X25519MLKEM768、SecP256r1MLKEM768、SecP384r1MLKEM1024、MLKEM1024、X25519、CurveP256、CurveP384、CurveP521
	// 留空则使用默认值（Go 1.27 默认包含 X25519MLKEM768 等后量子混合密钥交换）
	CurvePreferences []string `json:"curve_preferences" yaml:"curve-preferences"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{
			TLS: TLSConfig{
				Verify:         true,
				VerifyHostName: true,
				Fingerprint:    "",
				ALPN:           []string{"http/1.1"},
			},
		}
	})
}
