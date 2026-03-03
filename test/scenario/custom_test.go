package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	_ "github.com/Potterli20/trojan-go-fork/proxy/custom"
	"github.com/Potterli20/trojan-go-fork/test/util"
)

// generateCertFiles generates the server.crt and server.key files if they don't exist
func generateCertFiles() {
	fmt.Println("generateCertFiles called")
	// Get project root directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory:", err)
		return
	}
	fmt.Println("Current working directory:", cwd)

	// Go up two directories to get to project root
	projectRoot := filepath.Dir(filepath.Dir(cwd))
	fmt.Println("Project root directory:", projectRoot)

	// Generate certificate files in the project root directory
	certPath := filepath.Join(projectRoot, "server.crt")
	keyPath := filepath.Join(projectRoot, "server.key")
	fmt.Println("Certificate path:", certPath)
	fmt.Println("Key path:", keyPath)

	_, err1 := os.Stat(certPath)
	_, err2 := os.Stat(keyPath)
	fmt.Println("server.crt exists:", !os.IsNotExist(err1))
	fmt.Println("server.key exists:", !os.IsNotExist(err2))

	if os.IsNotExist(err1) || os.IsNotExist(err2) {
		fmt.Println("Generating certificate files...")
		// test key and cert
		cert := `-----BEGIN CERTIFICATE-----
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
-----END CERTIFICATE-----`
		key := `-----BEGIN PRIVATE KEY-----
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
-----END PRIVATE KEY-----`

		// Write to project root directory
		err := os.WriteFile(certPath, []byte(cert), 0o644)
		if err != nil {
			fmt.Println("Error writing server.crt:", err)
		} else {
			fmt.Println("Successfully wrote server.crt")
		}
		err = os.WriteFile(keyPath, []byte(key), 0o644)
		if err != nil {
			fmt.Println("Error writing server.key:", err)
		} else {
			fmt.Println("Successfully wrote server.key")
		}

		// Verify files were created
		_, err1 = os.Stat(certPath)
		_, err2 = os.Stat(keyPath)
		fmt.Println("After generation - server.crt exists:", !os.IsNotExist(err1))
		fmt.Println("After generation - server.key exists:", !os.IsNotExist(err2))
	} else {
		fmt.Println("Certificate files already exist")
	}
}

func TestCustom1(t *testing.T) {
	generateCertFiles()
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")

	// Client configuration
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - "12345678"
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
`, socksPort, serverPort)

	// Server configuration
	httpPort := common.PickPort("tcp", "127.0.0.1")
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
disable-http-check: true
password:
    - "12345678"
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
`, serverPort, httpPort)

	fmt.Println("Client configuration:")
	fmt.Println(clientData)
	fmt.Println("Server configuration:")
	fmt.Println(serverData)

	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fail()
	}
}

func TestCustom2(t *testing.T) {
	generateCertFiles()
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	adapterPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: custom
log-level: 0

inbound:
  node:
    - protocol: adapter
      tag: adapter
      config:
        local-addr: 127.0.0.1
        local-port: %d
    - protocol: socks
      tag: socks
      config:
        local-addr: 127.0.0.1
        local-port: %d
  path:
    -
      - adapter
      - socks

outbound:
  node:
    - protocol: transport
      tag: transport
      config:
        remote-addr: 127.0.0.1
        remote-port: %d

    - protocol: tls
      tag: tls
      config:
        ssl:
          sni: localhost
          verify: false

    - protocol: trojan
      tag: trojan
      config:
        password:
          - "12345678"

  path:
    - 
      - transport
      - tls
      - trojan

`, adapterPort, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: custom
log-level: 0

inbound:
  node:
    - protocol: transport
      tag: transport
      config:
        local-addr: 127.0.0.1
        local-port: %d

    - protocol: tls
      tag: tls
      config:
        ssl:
          sni: localhost
          key: server.key
          cert: server.crt

    - protocol: trojan
      tag: trojan
      config:
        disable-http-check: true
        password:
          - "12345678"

  path:
    - 
      - transport
      - tls
      - trojan

outbound:
  node:
    - protocol: transport
      tag: transport
      config:
        remote-addr: 127.0.0.1
        remote-port: %s
    - protocol: freedom
      tag: freedom

  path:
    - 
      - transport
      - freedom

`, serverPort, util.HTTPPort)

	if !CheckClientServer(clientData, serverData, adapterPort) {
		t.Fail()
	}
}
