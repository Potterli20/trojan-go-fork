package tls

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/p4gefau1t/trojan-go/common"
	"github.com/p4gefau1t/trojan-go/config"
	"github.com/p4gefau1t/trojan-go/test/util"
	"github.com/p4gefau1t/trojan-go/tunnel/freedom"
	"github.com/p4gefau1t/trojan-go/tunnel/transport"
)

var rsa2048Cert = `
-----BEGIN CERTIFICATE-----
MIIDZTCCAk2gAwIBAgIQBIBNupbq+KyHd0S1pzXEQDANBgkqhkiG9w0BAQsFADAR
MQ8wDQYDVQQDDAZmcmVnaWUwIBcNMjExMDE1MDI0NjU5WhgPMjEyMTA5MjEwMjQ2
NTlaMBQxEjAQBgNVBAMMCWxvY2FsaG9zdDCCASIwDQYJKoZIhvcNAQEBBQADggEP
ADCCAQoCggEBAK274VfScpxWVf1Ulqpn8kY00cnB21fDjSr+t5SHEC8kzii7s0Wv
goGRLKGN0e7ok5Ufvc6vdgl0LCMYHii0a0xiRRKFmy5eTpcFaIa+RqRUt696afMh
q4qalB9pIx8PRfME8VhVwF9p9hRpcQOiXT1vV4JJqNZH5QIVC6n1pxDToTEKojYl
dBAYixniZFSGqmu6tki3s5dUnTyyIkdeoIUw+GFZfBY531Yh4dLge/OLIsf8ei2L
dV17Yp+8xgEzs6AAX2BSoj7C+G9cOAJOabcKPs7e+BQmqg8JWlQL+5wt0Nuu049O
uB/f9aJNZEvkuEyM3ZX1LLyLqwvLXuzABS8CAwEAAaOBszCBsDAJBgNVHRMEAjAA
MB0GA1UdDgQWBBQrj8OKLxmucf8DDW5bpCRV1dop3TBMBgNVHSMERTBDgBTJJdOJ
89Rd2u+plS2ikWTd1JAcu6EVpBMwETEPMA0GA1UEAwwGZnJlZ2llghREgytcTE5Q
JCUuH9oYchyboT+kXDATBgNVHSUEDDAKBggrBgEFBQcDATALBgNVHQ8EBAMCBaAw
FAYDVR0RBA0wC4IJbG9jYWxob3N0MA0GCSqGSIb3DQEBCwUAA4IBAQBsGS0N1yCc
kBUaFMJ8CgiNQgLFoVJkawcT38//R+ii8/6Azt0JcTRzkcsm8Fut0ZrgilnlIGBI
3Y8dVNKDnDwlsANe/YFtIVJ7S/inK3SdZYyLec0VosTWT+T+NZLlI2ESLcSooVL6
+8cQAOcd6MArGHzOVam/pLTyatbOHv8ZFoNBT7zAEvUZJ7ZxwANLyW2XkiqC0Dlr
X/N2PHKY9Az94T4EhWJopiZA09HNAcQckAnAbHRoMulT94hxqyzV8RYDzHA9Z2TN
IfgNuFIhVyQqBp8Hc0/c8T0rBTYlbSPvggywQ9w5xQaoUePm31J/4jPSiFCci681
KnkNNFtbgo96
-----END CERTIFICATE-----
`

var rsa2048Key string = `
-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCtu+FX0nKcVlX9
VJaqZ/JGNNHJwdtXw40q/reUhxAvJM4ou7NFr4KBkSyhjdHu6JOVH73Or3YJdCwj
GB4otGtMYkUShZsuXk6XBWiGvkakVLevemnzIauKmpQfaSMfD0XzBPFYVcBfafYU
aXEDol09b1eCSajWR+UCFQup9acQ06ExCqI2JXQQGIsZ4mRUhqprurZIt7OXVJ08
siJHXqCFMPhhWXwWOd9WIeHS4HvziyLH/Hoti3Vde2KfvMYBM7OgAF9gUqI+wvhv
XDgCTmm3Cj7O3vgUJqoPCVpUC/ucLdDbrtOPTrgf3/WiTWRL5LhMjN2V9Sy8i6sL
y17swAUvAgMBAAECggEAQtfZoI+AtzPki76C5Xdu2KIz4RtsB/1eEB/GhCffCzRu
+W8WT4ZygOVZNaM6FWB4f9Shk6cglAyVer8pw2F/MvlQOAsdpJ52QFa9W7JTvaA2
uBYyM3BN7tsAiIFMGQQoVpMdRG5hwJQlML9M0ygiFaQEGEW85wzsSHvObArux2Lz
A9+YSOLdGWjW8UPGoIaWvPYp0/apwB9WQAfrxeHktbrAAv3293z1qq4BfP/t5mEm
vzOZiLRNlyjo6wj2kdB2UyF9SIUGWu2cMSTl8AiW+idLpursF1cSPaPtJAQyV10P
W1Y4ZESOGgXexbVNQJ6U8YTWCXiXkaDdGI3B9eyPcQKBgQDVR+bm6EeOks+91lfL
L94T3M6hCc9Cvz1Bxif1Ahl1rp0FN2102P/Ovf3ArpYMFNl0liwB2QehBGvgmNw4
IowxqNJKYUwqr+CMZVB0DHpc1p9avI7ISEt8hI6wiQjvvhLhtqqwKw9zxCgLaVD4
JaNN/a5BPyT0CfwqVKGZc5jaGQKBgQDQiC901vJlFekMWgCDOwsEB3S1t8K0xyVA
pWSFw25MzYHE3sBxdk5T+KAcxBHL0dSAvMfiMGnlguo/LAbs2bMR9xnFvoWpQYqp
NjBddZ1l9XtKGpeMwwWMn6+5MfoeVv068MZU2bwMyWiIfKm/4SNBjd6GGgM/m/do
f0D5prFShwKBgENAVd2vloitYFYS1UIbiOIoePssda8tUCqCSi0miVKjSZ3QeXIj
zp2pKO9t3aBXnq31gFDg11f9Zeq/KImG00ABZLXRckvvg1WhyLmfCaJmhn78/+TF
bxAHWFrwCuJVw+xqRe4g1dGCI8JdZgupCpJYEG9Acu2EjH/oicAyRBtZAoGAF+l1
36UCOJsxlQwBAQtVMQoV1PyUZBxt5iLRCxGk5UOvfL66PIh/ZNueqI3HKKMQBg8f
sI0yp3HCoKnQxXoVMZsvJmC5fPYaC1s+YokpGlby40V3WVnHmh95i/fyIWaCNS8E
3xf0m1bBGN2KrYkIfOzitmfnNXUSAraM4dO+g6MCgYEAgk5D3ulLR7scj4MK0jLs
hnUxHrgchGCAsREHDBo+GH6yBmyxyO35Zb4fDJEX4Yc8Enu8UHGhG7++lwGMZqY9
clPb/1SkuEWD+nCLUnkA5KyqPskF2GFjT8be4TBDFtQHBydxZFDZHxMhn4JShCTT
xiGQ1YfbqPMbovNUt1m0Es8=
-----END PRIVATE KEY-----
`

var eccCert = `
-----BEGIN CERTIFICATE-----
MIICTDCCAfKgAwIBAgIQDtCrO8cNST2eY2tA/AGrsDAKBggqhkjOPQQDAjBeMQsw
CQYDVQQGEwJDTjEOMAwGA1UEChMFTXlTU0wxKzApBgNVBAsTIk15U1NMIFRlc3Qg
RUNDIC0gRm9yIHRlc3QgdXNlIG9ubHkxEjAQBgNVBAMTCU15U1NMLmNvbTAeFw0y
MTA5MTQwNjQ1MzNaFw0yNjA5MTMwNjQ1MzNaMCExCzAJBgNVBAYTAkNOMRIwEAYD
VQQDEwlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAASvYy/r7XR1
Y39lC2JpRJh582zR2CTNynbuolK9a1jsbXaZv+hpBlHkgzMHsWu7LY9Pnb/Dbp4i
1lRASOddD/rLo4HOMIHLMA4GA1UdDwEB/wQEAwIFoDAdBgNVHSUEFjAUBggrBgEF
BQcDAQYIKwYBBQUHAwIwHwYDVR0jBBgwFoAUWxGyVxD0fBhTy3tH4eKznRFXFCYw
YwYIKwYBBQUHAQEEVzBVMCEGCCsGAQUFBzABhhVodHRwOi8vb2NzcC5teXNzbC5j
b20wMAYIKwYBBQUHMAKGJGh0dHA6Ly9jYS5teXNzbC5jb20vbXlzc2x0ZXN0ZWNj
LmNydDAUBgNVHREEDTALgglsb2NhbGhvc3QwCgYIKoZIzj0EAwIDSAAwRQIgDQUa
GEdmKstLMHUmmPMGm/P9S4vvSZV2VHsb3+AEyIUCIQCdJpbyTCz+mEyskhwrGOw/
blh3WBONv6MBtqPpmgE1AQ==
-----END CERTIFICATE-----
`

var eccKey = `
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIB8G2suYKuBLoodNIwRMp3JPN1fcZxCt3kcOYIx4nbcPoAoGCCqGSM49
AwEHoUQDQgAEr2Mv6+10dWN/ZQtiaUSYefNs0dgkzcp27qJSvWtY7G12mb/oaQZR
5IMzB7Fruy2PT52/w26eItZUQEjnXQ/6yw==
-----END EC PRIVATE KEY-----
`

func TestDefaultTLSRSA2048(t *testing.T) {
	os.WriteFile("server-rsa2048.crt", []byte(rsa2048Cert), 0o777)
	os.WriteFile("server-rsa2048.key", []byte(rsa2048Key), 0o777)
	serverCfg := &Config{
		TLS: TLSConfig{
			VerifyHostName: true,
			CertCheckRate:  1,
			KeyPath:        "server-rsa2048.key",
			CertPath:       "server-rsa2048.crt",
		},
	}
	clientCfg := &Config{
		TLS: TLSConfig{
			Verify:      false,
			SNI:         "localhost",
			Fingerprint: "",
		},
	}
	sctx := config.WithConfig(context.Background(), Name, serverCfg)
	cctx := config.WithConfig(context.Background(), Name, clientCfg)

	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx := config.WithConfig(context.Background(), transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})
	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)
	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)
	common.Must(err)
	s, err := NewServer(sctx, tcpServer)
	common.Must(err)
	c, err := NewClient(cctx, tcpClient)
	common.Must(err)

	wg := sync.WaitGroup{}
	wg.Add(1)
	var conn1, conn2 net.Conn
	go func() {
		conn2, err = s.AcceptConn(nil)
		common.Must(err)
		wg.Done()
	}()
	conn1, err = c.DialConn(nil, nil)
	common.Must(err)

	common.Must2(conn1.Write([]byte("12345678\r\n")))
	wg.Wait()
	buf := [10]byte{}
	conn2.Read(buf[:])
	if !util.CheckConn(conn1, conn2) {
		t.Fail()
	}
	conn1.Close()
	conn2.Close()
}

func TestDefaultTLSECC(t *testing.T) {
	os.WriteFile("server-ecc.crt", []byte(eccCert), 0o777)
	os.WriteFile("server-ecc.key", []byte(eccKey), 0o777)
	serverCfg := &Config{
		TLS: TLSConfig{
			VerifyHostName: true,
			CertCheckRate:  1,
			KeyPath:        "server-ecc.key",
			CertPath:       "server-ecc.crt",
		},
	}
	clientCfg := &Config{
		TLS: TLSConfig{
			Verify:      false,
			SNI:         "localhost",
			Fingerprint: "",
		},
	}
	sctx := config.WithConfig(context.Background(), Name, serverCfg)
	cctx := config.WithConfig(context.Background(), Name, clientCfg)

	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx := config.WithConfig(context.Background(), transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})
	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)
	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)
	common.Must(err)
	s, err := NewServer(sctx, tcpServer)
	common.Must(err)
	c, err := NewClient(cctx, tcpClient)
	common.Must(err)

	wg := sync.WaitGroup{}
	wg.Add(1)
	var conn1, conn2 net.Conn
	go func() {
		conn2, err = s.AcceptConn(nil)
		common.Must(err)
		wg.Done()
	}()
	conn1, err = c.DialConn(nil, nil)
	common.Must(err)

	common.Must2(conn1.Write([]byte("12345678\r\n")))
	wg.Wait()
	buf := [10]byte{}
	conn2.Read(buf[:])
	if !util.CheckConn(conn1, conn2) {
		t.Fail()
	}
	conn1.Close()
	conn2.Close()
}

func TestUTLSRSA2048(t *testing.T) {
	os.WriteFile("server-rsa2048.crt", []byte(rsa2048Cert), 0o777)
	os.WriteFile("server-rsa2048.key", []byte(rsa2048Key), 0o777)
	fingerprints := []string{
		"chrome",
		"firefox",
		"ios",
	}
	for _, s := range fingerprints {
		serverCfg := &Config{
			TLS: TLSConfig{
				CertCheckRate: 1,
				KeyPath:       "server-rsa2048.key",
				CertPath:      "server-rsa2048.crt",
			},
		}
		clientCfg := &Config{
			TLS: TLSConfig{
				Verify:      false,
				SNI:         "localhost",
				Fingerprint: s,
			},
		}
		sctx := config.WithConfig(context.Background(), Name, serverCfg)
		cctx := config.WithConfig(context.Background(), Name, clientCfg)

		port := common.PickPort("tcp", "127.0.0.1")
		transportConfig := &transport.Config{
			LocalHost:  "127.0.0.1",
			LocalPort:  port,
			RemoteHost: "127.0.0.1",
			RemotePort: port,
		}
		ctx := config.WithConfig(context.Background(), transport.Name, transportConfig)
		ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})
		tcpClient, err := transport.NewClient(ctx, nil)
		common.Must(err)
		tcpServer, err := transport.NewServer(ctx, nil)
		common.Must(err)

		s, err := NewServer(sctx, tcpServer)
		common.Must(err)
		c, err := NewClient(cctx, tcpClient)
		common.Must(err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		var conn1, conn2 net.Conn
		go func() {
			conn2, err = s.AcceptConn(nil)
			common.Must(err)
			wg.Done()
		}()
		conn1, err = c.DialConn(nil, nil)
		common.Must(err)

		common.Must2(conn1.Write([]byte("12345678\r\n")))
		wg.Wait()
		buf := [10]byte{}
		conn2.Read(buf[:])
		if !util.CheckConn(conn1, conn2) {
			t.Fail()
		}
		conn1.Close()
		conn2.Close()
		s.Close()
		c.Close()
	}
}

func TestUTLSECC(t *testing.T) {
	os.WriteFile("server-ecc.crt", []byte(eccCert), 0o777)
	os.WriteFile("server-ecc.key", []byte(eccKey), 0o777)
	fingerprints := []string{
		"chrome",
		"firefox",
		"ios",
	}
	for _, s := range fingerprints {
		serverCfg := &Config{
			TLS: TLSConfig{
				CertCheckRate: 1,
				KeyPath:       "server-ecc.key",
				CertPath:      "server-ecc.crt",
			},
		}
		clientCfg := &Config{
			TLS: TLSConfig{
				Verify:      false,
				SNI:         "localhost",
				Fingerprint: s,
			},
		}
		sctx := config.WithConfig(context.Background(), Name, serverCfg)
		cctx := config.WithConfig(context.Background(), Name, clientCfg)

		port := common.PickPort("tcp", "127.0.0.1")
		transportConfig := &transport.Config{
			LocalHost:  "127.0.0.1",
			LocalPort:  port,
			RemoteHost: "127.0.0.1",
			RemotePort: port,
		}
		ctx := config.WithConfig(context.Background(), transport.Name, transportConfig)
		ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})
		tcpClient, err := transport.NewClient(ctx, nil)
		common.Must(err)
		tcpServer, err := transport.NewServer(ctx, nil)
		common.Must(err)

		s, err := NewServer(sctx, tcpServer)
		common.Must(err)
		c, err := NewClient(cctx, tcpClient)
		common.Must(err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		var conn1, conn2 net.Conn
		go func() {
			conn2, err = s.AcceptConn(nil)
			common.Must(err)
			wg.Done()
		}()
		conn1, err = c.DialConn(nil, nil)
		common.Must(err)

		common.Must2(conn1.Write([]byte("12345678\r\n")))
		wg.Wait()
		buf := [10]byte{}
		conn2.Read(buf[:])
		if !util.CheckConn(conn1, conn2) {
			t.Fail()
		}
		conn1.Close()
		conn2.Close()
		s.Close()
		c.Close()
	}
}

func TestMatch(t *testing.T) {
	if !isDomainNameMatched("*.google.com", "www.google.com") {
		t.Fail()
	}

	if isDomainNameMatched("*.google.com", "google.com") {
		t.Fail()
	}

	if !isDomainNameMatched("localhost", "localhost") {
		t.Fail()
	}
}
