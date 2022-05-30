package scenario

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sync"
	"testing"
	"time"

	netproxy "golang.org/x/net/proxy"
	"google.golang.org/genproto/googleapis/cloud/oslogin/v1"

	_ "github.com/Potterli20/trojan-go-fork/api"
	_ "github.com/Potterli20/trojan-go-fork/api/service"
	"github.com/Potterli20/trojan-go-fork/common"
	_ "github.com/Potterli20/trojan-go-fork/log/golog"
	"github.com/Potterli20/trojan-go-fork/proxy"
	_ "github.com/Potterli20/trojan-go-fork/proxy/client"
	_ "github.com/Potterli20/trojan-go-fork/proxy/forward"
	_ "github.com/Potterli20/trojan-go-fork/proxy/nat"
	_ "github.com/Potterli20/trojan-go-fork/proxy/server"
	_ "github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// test key and cert

var cert = `
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

var key = `
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

func init() {
	os.WriteFile("server.crt", []byte(cert), 0o777)
	os.WriteFile("server.key", []byte(key), 0o777)q
}

func CheckClientServer(clientData, serverData string, socksPort int) (ok bool) {
	trojan.Auth = nil
	server, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	common.Must(err)
	go server.Run()

	client, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
	common.Must(err)
	go client.Run()

	time.Sleep(time.Second * 2)
	dialer, err := netproxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, netproxy.Direct)
	common.Must(err)

	ok = true
	const num = 100
	wg := sync.WaitGroup{}
	wg.Add(num)
	for i := 0; i < num; i++ {
		go func() {
			const payloadSize = 1024
			payload := util.GeneratePayload(payloadSize)
			buf := [payloadSize]byte{}

			conn, err := dialer.Dial("tcp", util.EchoAddr)
			common.Must(err)

			common.Must2(conn.Write(payload))
			common.Must2(conn.Read(buf[:]))

			if !bytes.Equal(payload, buf[:]) {
				ok = false
			}
			conn.Close()
			wg.Done()
		}()
	}
	wg.Wait()
	client.Close()
	server.Close()
	return
}

func TestClientServerWebsocketSubTree(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
websocket:
    enabled: true
    path: /ws
    host: somedomainname.com
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
websocket:
    enabled: true
    path: /ws
    host: 127.0.0.1
`, serverPort, util.HTTPPort)

	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fail()
	}
}

func TestClientServerTrojanSubTree(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
`, serverPort, util.HTTPPort)

	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fail()
	}
}

func TestWebsocketDetection(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")

	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
websocket:
    enabled: true
    path: /ws
    hostname: 127.0.0.1
`, serverPort, util.HTTPPort)

	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fail()
	}
}

func TestPluginWebsocket(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")

	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
transport-plugin:
    enabled: true
    type: plaintext
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
websocket:
    enabled: true
    path: /ws
    hostname: 127.0.0.1
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
transport-plugin:
    enabled: true
    type: plaintext
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
websocket:
    enabled: true
    path: /ws
    hostname: 127.0.0.1
`, serverPort, util.HTTPPort)

	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fail()
	}
}

func TestForward(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	clientPort := common.PickPort("tcp", "127.0.0.1")
	_, targetPort, _ := net.SplitHostPort(util.EchoAddr)
	clientData := fmt.Sprintf(`
run-type: forward
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
target-addr: 127.0.0.1
target-port: %s
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
websocket:
    enabled: true
    path: /ws
    hostname: 127.0.0.1
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, clientPort, serverPort, targetPort)
	go func() {
		proxy, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
		common.Must(err)
		common.Must(proxy.Run())
	}()

	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: "localhost"
websocket:
    enabled: true
    path: /ws
    hostname: 127.0.0.1
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
`, serverPort, util.HTTPPort)
	go func() {
		proxy, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
		common.Must(err)
		common.Must(proxy.Run())
	}()

	time.Sleep(time.Second * 2)

	payload := util.GeneratePayload(1024)
	buf := [1024]byte{}

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort))
	common.Must(err)

	common.Must2(conn.Write(payload))
	common.Must2(conn.Read(buf[:]))

	if !bytes.Equal(payload, buf[:]) {
		t.Fail()
	}

	packet, err := net.ListenPacket("udp", "")
	common.Must(err)
	common.Must2(packet.WriteTo(payload, &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: clientPort,
	}))
	_, _, err = packet.ReadFrom(buf[:])
	common.Must(err)
	if !bytes.Equal(payload, buf[:]) {
		t.Fail()
	}
}

func TestLeak(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
log-level: 0
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
api:
    enabled: true
    api-port: 0
`, socksPort, serverPort)
	client, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
	common.Must(err)
	go client.Run()
	time.Sleep(time.Second * 3)
	client.Close()
	time.Sleep(time.Second * 3)
	// http.ListenAndServe("localhost:6060", nil)
}

func SingleThreadBenchmark(clientData, serverData string, socksPort int) {
	server, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
	common.Must(err)
	go server.Run()

	client, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	common.Must(err)
	go client.Run()

	time.Sleep(time.Second * 2)
	dialer, err := netproxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, netproxy.Direct)
	common.Must(err)

	const num = 100
	wg := sync.WaitGroup{}
	wg.Add(num)
	const payloadSize = 1024 * 1024 * 1024
	payload := util.GeneratePayload(payloadSize)

	for i := 0; i < 100; i++ {
		conn, err := dialer.Dial("tcp", util.BlackHoleAddr)
		common.Must(err)

		t1 := time.Now()
		common.Must2(conn.Write(payload))
		t2 := time.Now()

		speed := float64(payloadSize) / (float64(t2.Sub(t1).Nanoseconds()) / float64(time.Second))
		fmt.Printf("speed: %f Gbps\n", speed/1024/1024/1024)

		conn.Close()
	}
	client.Close()
	server.Close()
}

func BenchmarkClientServer(b *testing.B) {
	go func() {
		fmt.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
log-level: 0
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
log-level: 0
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
`, serverPort, util.HTTPPort)

	SingleThreadBenchmark(clientData, serverData, socksPort)
}
