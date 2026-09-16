package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
)

func TestOutboundConfigAPI(t *testing.T) {
	ctx := t.Context()
	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{}})
	port := common.PickPort("tcp", "127.0.0.1")
	ctx = config.WithConfig(ctx, Name, &Config{
		APIConfig{Enabled: true, APIHost: "127.0.0.1", APIPort: port},
	})
	auth, err := memory.NewAuthenticator(ctx)
	common.Must(err)
	go RunServerAPI(ctx, auth)
	time.Sleep(time.Second * 2)

	freedom.SetGlobalOutbound(nil, 0)
	defer freedom.SetGlobalOutbound(nil, 0)

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	common.Must(err)
	defer conn.Close()
	client := NewTrojanServerServiceClient(conn)

	setResp, err := client.SetOutboundConfig(ctx, &SetOutboundConfigRequest{LocalAddr: "127.0.0.2", Fwmark: 0x1234})
	common.Must(err)
	if !setResp.Success {
		t.Fatalf("SetOutboundConfig failed: %s", setResp.Info)
	}
	ip, mark := freedom.GetGlobalOutbound()
	if ip == nil || ip.String() != "127.0.0.2" {
		t.Fatalf("local_addr not set in freedom: got %v", ip)
	}
	if mark != 0x1234 {
		t.Fatalf("fwmark not set in freedom: got %d", mark)
	}

	getResp, err := client.GetOutboundConfig(ctx, &GetOutboundConfigRequest{})
	common.Must(err)
	if getResp.LocalAddr != "127.0.0.2" || getResp.Fwmark != 0x1234 {
		t.Fatalf("GetOutboundConfig mismatch: %+v", getResp)
	}

	setResp, err = client.SetOutboundConfig(ctx, &SetOutboundConfigRequest{LocalAddr: "not-an-ip"})
	common.Must(err)
	if setResp.Success {
		t.Fatal("expected failure for invalid IP")
	}
	ip, mark = freedom.GetGlobalOutbound()
	if ip == nil || ip.String() != "127.0.0.2" || mark != 0x1234 {
		t.Fatal("failed SetOutboundConfig must not modify state")
	}

	setResp, err = client.SetOutboundConfig(ctx, &SetOutboundConfigRequest{LocalAddr: "", Fwmark: 0})
	common.Must(err)
	if !setResp.Success {
		t.Fatalf("clear failed: %s", setResp.Info)
	}
	ip, mark = freedom.GetGlobalOutbound()
	if ip != nil || mark != 0 {
		t.Fatalf("clear did not reset state: ip=%v mark=%d", ip, mark)
	}

	getResp, err = client.GetOutboundConfig(ctx, &GetOutboundConfigRequest{})
	common.Must(err)
	if getResp.LocalAddr != "" || getResp.Fwmark != 0 {
		t.Fatalf("GetOutboundConfig after clear: %+v", getResp)
	}
}

func TestServerAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = config.WithConfig(ctx, memory.Name,
		&memory.Config{
			Passwords: []string{},
		})
	port := common.PickPort("tcp", "127.0.0.1")
	ctx = config.WithConfig(ctx, Name, &Config{
		APIConfig{
			Enabled: true,
			APIHost: "127.0.0.1",
			APIPort: port,
		},
	})
	auth, err := memory.NewAuthenticator(ctx)
	common.Must(err)
	go RunServerAPI(ctx, auth)
	time.Sleep(time.Second * 3)
	common.Must(auth.AddUser("hash1234"))
	_, user := auth.AuthUser("hash1234")
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	common.Must(err)
	server := NewTrojanServerServiceClient(conn)
	stream1, err := server.ListUsers(ctx, &ListUsersRequest{})
	common.Must(err)
	for {
		resp, err := stream1.Recv()
		if err != nil {
			break
		}
		fmt.Println(resp.Status.User.Hash)
		if resp.Status.User.Hash != "hash1234" {
			t.Fail()
		}
		fmt.Println(resp.Status.SpeedCurrent)
		fmt.Println(resp.Status.SpeedLimit)
	}
	stream1.CloseSend()
	user.AddSentTraffic(1234)
	user.AddRecvTraffic(5678)
	time.Sleep(time.Second * 1)
	stream2, err := server.GetUsers(ctx)
	common.Must(err)
	stream2.Send(&GetUsersRequest{
		User: &User{
			Hash: "hash1234",
		},
	})
	resp2, err := stream2.Recv()
	common.Must(err)
	if resp2.Status.TrafficTotal.DownloadTraffic != 1234 || resp2.Status.TrafficTotal.UploadTraffic != 5678 {
		t.Fatal("wrong traffic")
	}

	stream3, err := server.SetUsers(ctx)
	common.Must(err)
	stream3.Send(&SetUsersRequest{
		Status: &UserStatus{
			User: &User{
				Hash: "hash1234",
			},
		},
		Operation: SetUsersRequest_Delete,
	})
	resp3, err := stream3.Recv()
	if err != nil || !resp3.Success {
		t.Fatal("user not exists")
	}
	valid, _ := auth.AuthUser("hash1234")
	if valid {
		t.Fatal("failed to auth")
	}
	stream3.Send(&SetUsersRequest{
		Status: &UserStatus{
			User: &User{
				Hash: "newhash",
			},
		},
		Operation: SetUsersRequest_Add,
	})
	resp3, err = stream3.Recv()
	if err != nil || !resp3.Success {
		t.Fatal("failed to read")
	}
	valid, user = auth.AuthUser("newhash")
	if !valid {
		t.Fatal("failed to auth 2")
	}
	stream3.Send(&SetUsersRequest{
		Status: &UserStatus{
			User: &User{
				Hash: "newhash",
			},
			SpeedLimit: &Speed{
				DownloadSpeed: 5000,
				UploadSpeed:   3000,
			},
			TrafficTotal: &Traffic{
				DownloadTraffic: 1,
				UploadTraffic:   1,
			},
		},
		Operation: SetUsersRequest_Modify,
	})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			user.AddSentTraffic(200)
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			user.AddRecvTraffic(300)
		}
	}()
	time.Sleep(time.Second * 3)
	for range 3 {
		stream2.Send(&GetUsersRequest{
			User: &User{
				Hash: "newhash",
			},
		})
		resp2, err = stream2.Recv()
		common.Must(err)
		fmt.Println(resp2.Status.SpeedCurrent)
		fmt.Println(resp2.Status.SpeedLimit)
		time.Sleep(time.Second)
	}
	stream2.CloseSend()
	cancel()
}

func TestTLSRSA(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	cfg := &Config{
		API: APIConfig{
			Enabled: true,
			APIHost: "127.0.0.1",
			APIPort: port,
			SSL: SSLConfig{
				Enabled:        true,
				CertPath:       "server-rsa2048.crt",
				KeyPath:        "server-rsa2048.key",
				VerifyClient:   false,
				ClientCertPath: []string{"client-rsa2048.crt"},
			},
		},
	}

	ctx := config.WithConfig(context.Background(), Name, cfg)
	ctx = config.WithConfig(ctx, memory.Name,
		&memory.Config{
			Passwords: []string{},
		})

	auth, err := memory.NewAuthenticator(ctx)
	common.Must(err)
	go func() {
		common.Must(RunServerAPI(ctx, auth))
	}()
	time.Sleep(time.Second)
	pool := x509.NewCertPool()
	certBytes, err := os.ReadFile("server-rsa2048.crt")
	common.Must(err)
	pool.AppendCertsFromPEM(certBytes)

	certificate, err := tls.LoadX509KeyPair("client-rsa2048.crt", "client-rsa2048.key")
	common.Must(err)
	creds := credentials.NewTLS(&tls.Config{
		ServerName:   "localhost",
		RootCAs:      pool,
		Certificates: []tls.Certificate{certificate},
	})
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(creds))
	common.Must(err)
	server := NewTrojanServerServiceClient(conn)
	stream, err := server.ListUsers(ctx, &ListUsersRequest{})
	common.Must(err)
	stream.CloseSend()
	conn.Close()
}

func TestTLSECC(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	cfg := &Config{
		API: APIConfig{
			Enabled: true,
			APIHost: "127.0.0.1",
			APIPort: port,
			SSL: SSLConfig{
				Enabled:        true,
				CertPath:       "server-ecc.crt",
				KeyPath:        "server-ecc.key",
				VerifyClient:   false,
				ClientCertPath: []string{"client-ecc.crt"},
			},
		},
	}

	ctx := config.WithConfig(context.Background(), Name, cfg)
	ctx = config.WithConfig(ctx, memory.Name,
		&memory.Config{
			Passwords: []string{},
		})

	auth, err := memory.NewAuthenticator(ctx)
	common.Must(err)
	go func() {
		common.Must(RunServerAPI(ctx, auth))
	}()
	time.Sleep(time.Second)
	pool := x509.NewCertPool()
	certBytes, err := os.ReadFile("server-ecc.crt")
	common.Must(err)
	pool.AppendCertsFromPEM(certBytes)

	certificate, err := tls.LoadX509KeyPair("client-ecc.crt", "client-ecc.key")
	common.Must(err)
	creds := credentials.NewTLS(&tls.Config{
		ServerName:   "localhost",
		RootCAs:      pool,
		Certificates: []tls.Certificate{certificate},
	})
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(creds))
	common.Must(err)
	server := NewTrojanServerServiceClient(conn)
	stream, err := server.ListUsers(ctx, &ListUsersRequest{})
	common.Must(err)
	stream.CloseSend()
	conn.Close()
}

var serverRSA2048Cert = `
-----BEGIN CERTIFICATE-----
MIIDJTCCAg2gAwIBAgIUVqT+m3AvjDX38HlwZBr1Vh5oKyUwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDkxNjAxMzAzNVoXDTM2MDkx
MzAxMzAzNVowFDESMBAGA1UEAwwJbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAoxvC+hwmpIiVWfmHBEDannP2IhvGoSjXJrVEePefo9Zb
xbzkamj1QzLvtGqfL5bzNgmP6p7ig/CbNQ/x7w7sWwtZ7YeQkPKVEI0FojEMZLW0
GdMkvNgAWrwN54bJeqGQZUghxPjp0c2qXUztjwdtnsn0yBEbfjsePkzINGuicC2r
JzXUnfyguAep+o0LCRHwCJZwaUZniVEXNq8xNnqpI4oVdPMUMSa75MBsPB3Qx6lM
TduHd0dm348dfFoQ2YymZDXrKc/j0pSeEIw2aMM1u8PBflTjhO3iIH4fWnz3BGSo
imQ3hMlhAZk0RelWzs77pZDORCU4gRcFCX1t2z9uJwIDAQABo28wbTAdBgNVHQ4E
FgQUaVsyXXXcqANGZeQW/xXqkQGqNn0wHwYDVR0jBBgwFoAUaVsyXXXcqANGZeQW
/xXqkQGqNn0wDwYDVR0TAQH/BAUwAwEB/zAaBgNVHREEEzARgglsb2NhbGhvc3SH
BH8AAAEwDQYJKoZIhvcNAQELBQADggEBAAb9X7aVu//xj9t4fgXF4SBTXsZ6oHbZ
gzKrdhKzTrrPj+U602l7Rx4ChcDhspMLm+qNnnLEK3nzyaaYloMesZwwxlKwK7GA
9RMgVL/EsBNmwKpVP9LEuWSEFmRn3Ovw7dV68vXyroYQ546Dtzi9JYj71E2e/zxE
OP6ULOUUnSM7hSPtLGzMJXHei9h3Kz42LdJ2/RRMtId+RwwbSXo0UnRh/jl932lP
44uR84Znx6OHcujOBStSF3nX0CgowXFC6oRHEBJBf3AZLx9gIaC3sGDD3Yu6Yi4T
MDCkjP4pImspVkSJGoOiQO6QVvgqaOCNQq52gU4K3Tsj/6WZwN3rEjo=
-----END CERTIFICATE-----
`

var serverRSA2048Key = `
-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCjG8L6HCakiJVZ
+YcEQNqec/YiG8ahKNcmtUR495+j1lvFvORqaPVDMu+0ap8vlvM2CY/qnuKD8Js1
D/HvDuxbC1nth5CQ8pUQjQWiMQxktbQZ0yS82ABavA3nhsl6oZBlSCHE+OnRzapd
TO2PB22eyfTIERt+Ox4+TMg0a6JwLasnNdSd/KC4B6n6jQsJEfAIlnBpRmeJURc2
rzE2eqkjihV08xQxJrvkwGw8HdDHqUxN24d3R2bfjx18WhDZjKZkNespz+PSlJ4Q
jDZowzW7w8F+VOOE7eIgfh9afPcEZKiKZDeEyWEBmTRF6VbOzvulkM5EJTiBFwUJ
fW3bP24nAgMBAAECggEAJhN+Aar+rlwgGV/xz+Ff56uEYtP/G3IQP74DHQYZY0LQ
K6n73Idh8ez7Hi/ht1dSsWOsEAZFNK2/XbS6MqdWL67HsHZ8HgozGCkDjqhKj1wH
jhvHMLpv4r6RtGG3uQPsVGhxUa0V0F9ljOA/eKswQOg+V1H/DAm69qov9mTGB8+R
goMwPeWu1GxAy4rExzawO5NAf/DEpYNzMKKvUE+0vfKajhsoooFhrMv7Q2vtIJY6
mM2QZdKJ1UwsZb3X6l+zchP8RX7w7DOhkgRNcYCxVEwqfPLy0ma7kfnB8Lst6RCF
M5+DSJ1ZYdUsdX+S9nayjYvc7RaZJ7wIwgRPTpzAkQKBgQDigjUvNDcZ0MdIP33Y
AG1LfyZRRu7VrpdBXXfQSNHAasesb5/zSRCJS/1qb8j9qvdTHlESuAOnp0OF9csY
Bx/lGnRJy99l5896ZMoBXnqwk2MGqJpkDJpchcRQ4Wj5swoGQV2zjyyitEC1OtME
++H4e4aXNnUpZu/TrzjDbk6fzwKBgQC4WFpoHkosyzBMLo8Eru3XuBTxDEQ/+XrS
yO/lRUpzIIyWjnUZ4mFkrQxMDZITTvjm6aCOoHXEnFetpfIfFkRuZVvaiTUsoAwk
S8NJgg6CXQug9qsw4qw1OS7W4azAjkWpshSsWd3V02s7g5CqG3PuZPNEGyp1Kyrh
77Xo+apKKQKBgQCgCgHL7We1LMdxK7MdyAdxHVCUgrrDbc1fGMxL9PeGrauREXmB
KrGtYGyYJI1tdbu0FaqQwEWM0miqIOWzf20wscVSUuLwHJ6Cyu3Tk866Lhj8nmof
oKv8DWONBgbmznKZwtuSv+l4uEU0B3ELh3C84YJSGt8jNqDi/13q72hMDwKBgH/h
Ur39nSkbvyJp/e6axwWpfmWFQ+c5UtncaIacnbvlrYFXy6YsI7HqVaiAlX/tfb75
/NZUO74rUqt0fdTQ5qHKkIC2Q/vz/thC8nMQ1K3cjA+abkFYTWwSggqwvN1LFMpW
bf7tzHTj1/SOHRHUP4T15MevLLHhQzs+xeubHmWZAoGBAILHjdcwBAZiHc/DM3Nf
9+gO1fqNpdYL81iLXvOeoIOYFp07nLIBuoSjJBEPDjMhPLGvO8RHhiBwZFjSTT/4
a9VDDW7mmIGUzeaPVBEC4M1FJ1G8l1RVxykACbl51XXRPwhaJMSJyFaXo/6YwNKM
9ck1YR+gCZz5RttHn5D6SC3F
-----END PRIVATE KEY-----
`

var clientRSA2048Cert = `
-----BEGIN CERTIFICATE-----
MIIDJTCCAg2gAwIBAgIUKLWHPcA0qeXf6Zf5vDbDnc14k6AwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDkxNjAxMzA0OVoXDTM2MDkx
MzAxMzA0OVowFDESMBAGA1UEAwwJbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAontCCMH790ciYJtbIzhHHz4O+H1PyS4QQnIT6UK8CLA0
+CCzR0dB/LlNISH2d2rqu5XhzHcgT839WA5tpDvL/krLq2d4iy+Vlo5QInDul6BS
zJSm/xJhXauzZ+s/Zr2vG8KFO/bF9wKv5+7+vNexEir2gLT9779tFhu5Fex3GcNy
s6Ix8hfV8ZYO5L2I8oWY1vVHDtPZRE6CIA7gyFdnqV9uXMc0UM2cbhokyKGECA2h
S0PGNaCBfr5XTJkVwmoCBEoeJMeN1lmqdnX5egvU6+STR66we8Nhg4b/REjMmXcG
aiXVvi/olDeAvaoWJpSQiO2TPrQk/1nXNps+m2Uk3wIDAQABo28wbTAdBgNVHQ4E
FgQUagcnpmP0ClGS3vvDbYi7/znEwnswHwYDVR0jBBgwFoAUagcnpmP0ClGS3vvD
bYi7/znEwnswDwYDVR0TAQH/BAUwAwEB/zAaBgNVHREEEzARgglsb2NhbGhvc3SH
BH8AAAEwDQYJKoZIhvcNAQELBQADggEBAFHWWTFO+PhZ7DYmd4yXalGK8GNGfYjN
YDbBQYY/OXpaNw/A3xEq8QyTxHl+RdSAFD7PeSvLu/JIvHNhSf9yh/o8xpXVS+i+
M7DweA4I8Jmaqi4SlvocAkoIMVPVpWAQ6A5TvK2KWjb7VbEihDRhGE6AqtJOFez8
NEqyzxqscK2uC6EmBfuLrfuaeVEjK8VCBjjrSVknqShLUVRhNwIIzetrlbX6L81L
IC8fVUFrT/FEPusxomrRmJYUY16bqd74edaSfi+Gj61QuPxp/v9kypQTciU0kRCv
bQjp8z4eB+k/qvd0Pj7SSFm/dT82Hv8lSoAv5Qpw2wsxlikNh9A2gKQ=
-----END CERTIFICATE-----
`

var clientRSA2048Key = `
-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCie0IIwfv3RyJg
m1sjOEcfPg74fU/JLhBCchPpQrwIsDT4ILNHR0H8uU0hIfZ3auq7leHMdyBPzf1Y
Dm2kO8v+SsurZ3iLL5WWjlAicO6XoFLMlKb/EmFdq7Nn6z9mva8bwoU79sX3Aq/n
7v6817ESKvaAtP3vv20WG7kV7HcZw3KzojHyF9Xxlg7kvYjyhZjW9UcO09lEToIg
DuDIV2epX25cxzRQzZxuGiTIoYQIDaFLQ8Y1oIF+vldMmRXCagIESh4kx43WWap2
dfl6C9Tr5JNHrrB7w2GDhv9ESMyZdwZqJdW+L+iUN4C9qhYmlJCI7ZM+tCT/Wdc2
mz6bZSTfAgMBAAECggEAFhz0VcDLi/yQadl67oYq7SOYYf/xVeNWWdSwxQmiEa9O
5jN9HXGjQhlDWAOz9CNcTaoOZbITdwkFNnDFl0PUIDMJw7V8qw9k1RgKlpq2vh1s
Z2TBKEHWIoDLFT+SYhHlZ7/HkOulnFfZ8j+NR8zgenqCZe3mtDRh96T6QZ5BxK2k
wpJEHKr1TWDIBjJWxM7oK3QH8tWYePK8ayOaHgs3m9IgVYtCUsfY8ktytUIWl4OR
vtVo6AHro9yDXfZTjTMHHBwdC7Lsm/ZmbvfrcT9fg/IoJbKelRKSEKQsEE1FH3Z4
d3hpIgqvH37FYTFh38Be9pp7W48GxpJyB4z5An4eoQKBgQDQumjtgYkrHU58fNgh
wfQbS6TYXeXEyux338YljROBBk6LhPC0h2bAzbfQXMcl4OckTg46y0yQM9acxAQK
iOKd+QHCDkjJgaG2e3uncxDvJ3rRP9uidvMnhkem3GYTB3fTc6c7Gb1mHa9Pn6xa
bcG8++ts23Yl9c8orMENN2dRlwKBgQDHR5GrB/AGOZM1XXn+JZgttHKQ+XGNu5eA
MxSrMBc2eXTDeNjReHosI+cwmbbvYDRmr5XzlLmr5e2gBMYPqIyuosX7im6INEG1
t0A08pj71GZZbTcQBIMtepJF46DSuu/ppCwKabWyEH2uWm8mocdTPtJfT8CUURa1
rP4haNaf+QKBgQCp4Czpr9VNa6qnEoxs3Qeo92WQWstX4LeX0F3ZzfmjtthSIL4u
j0yHemYxhHDoWSSFnaljHHaRnj76k2WelU2quDBAGZQPvBs7A0DeRX3wxjbk+o3s
qda3wkeqfBHvRtK1G5ltNkO5SkuWCCQj7NQS1Q0EKZD80BPrzL3J3d+OvQKBgQCN
ZLZyh7tCb2+Hmb+JF9uV8kannpV3Xvbru1Ka9BBEUoEKgKA2YCkvUHok/avSxIvp
oAPhRFFJKmcj9r8hNRI8hrm69Eng0lMdP9yKtObfJ6FHKjq7XrhEeId4lz3wxzqa
qCWnbcHBifniz7+1xWvMIPLbNZcKpU5bsVbPFbfS4QKBgGhedZLFEOjMFD5dPW7s
iOxbxueOTL4KTzcOvzYtFVQX0MuRTANlZKepMnjmw+KR2oubqCSJJ8LIlvuvg1zS
qktklvFXeBmyMPzSvWo1Bv3fce9OPtQucEnkEjJKi8dV2OOQQbqNKBOQTIvDuesx
ii/PVrV0jhwUQPhIfWwHmGZu
-----END PRIVATE KEY-----
`

var serverECCCert = `
-----BEGIN CERTIFICATE-----
MIIBmTCCAT+gAwIBAgIUa3VFlWtvq1gTwanZwmEqZylMY6QwCgYIKoZIzj0EAwIw
FDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDkxNjAxMzEwNFoXDTM2MDkxMzAx
MzEwNFowFDESMBAGA1UEAwwJbG9jYWxob3N0MFkwEwYHKoZIzj0CAQYIKoZIzj0D
AQcDQgAEVys+/PFQ30CB8o7Gx957wTJvaLl6GqjkrS/pfY+Xc+NDIDt1arJOAYKH
b3uqbq1N9okjE1yYaT64ZLc4Oiz4vqNvMG0wHQYDVR0OBBYEFKGXPbt7MBtqosSR
Stm80EcjYLPfMB8GA1UdIwQYMBaAFKGXPbt7MBtqosSRStm80EcjYLPfMA8GA1Ud
EwEB/wQFMAMBAf8wGgYDVR0RBBMwEYIJbG9jYWxob3N0hwR/AAABMAoGCCqGSM49
BAMCA0gAMEUCIDCy26uyScV2QGVh+KtBb2mmKd2xA27u/jTByVc4+ruWAiEA5HAG
A+UUzEXQhtLknPkkWqoICIhKbImhaZEP9GwJijY=
-----END CERTIFICATE-----
`

var serverECCKey = `
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIExs68kFb9l2kIi3eHw4pYH3VLCQpfty24fHgZXHV3hSoAoGCCqGSM49
AwEHoUQDQgAEVys+/PFQ30CB8o7Gx957wTJvaLl6GqjkrS/pfY+Xc+NDIDt1arJO
AYKHb3uqbq1N9okjE1yYaT64ZLc4Oiz4vg==
-----END EC PRIVATE KEY-----
`

var clientECCCert = `
-----BEGIN CERTIFICATE-----
MIIBmTCCAT+gAwIBAgIUOLc662imv8aqsRO8HKj13c5LfcswCgYIKoZIzj0EAwIw
FDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDkxNjAxMzExOFoXDTM2MDkxMzAx
MzExOFowFDESMBAGA1UEAwwJbG9jYWxob3N0MFkwEwYHKoZIzj0CAQYIKoZIzj0D
AQcDQgAEAaov6MjcLiHmICK9yQgmLRG5zZdsKWbfeZOHDHvFRD4ZT4eJHlTVIFgh
Oy6twXMQMD340qGk0Mez8tLjIsjlSqNvMG0wHQYDVR0OBBYEFFqXf4LZ77CsyZA9
Ru8HUNk/XbVPMB8GA1UdIwQYMBaAFFqXf4LZ77CsyZA9Ru8HUNk/XbVPMA8GA1Ud
EwEB/wQFMAMBAf8wGgYDVR0RBBMwEYIJbG9jYWxob3N0hwR/AAABMAoGCCqGSM49
BAMCA0gAMEUCIQDqf4G6f4Mx0h127ASFy+TPj7Rl381mPkWnn42+gW/NlAIgQP5b
00NnHJK2KIECZua2fQOiPzR5rTbSDLKtvvUTbD4=
-----END CERTIFICATE-----
`

var clientECCKey = `
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIAOEdB9yVy5w/T06feUGpEMuF/6qVqnskDFRNbGeucuEoAoGCCqGSM49
AwEHoUQDQgAEAaov6MjcLiHmICK9yQgmLRG5zZdsKWbfeZOHDHvFRD4ZT4eJHlTV
IFghOy6twXMQMD340qGk0Mez8tLjIsjlSg==
-----END EC PRIVATE KEY-----
`

func init() {
	os.WriteFile("server-rsa2048.crt", []byte(serverRSA2048Cert), 0o777)
	os.WriteFile("server-rsa2048.key", []byte(serverRSA2048Key), 0o777)
	os.WriteFile("client-rsa2048.crt", []byte(clientRSA2048Cert), 0o777)
	os.WriteFile("client-rsa2048.key", []byte(clientRSA2048Key), 0o777)

	os.WriteFile("server-ecc.crt", []byte(serverECCCert), 0o777)
	os.WriteFile("server-ecc.key", []byte(serverECCKey), 0o777)
	os.WriteFile("client-ecc.crt", []byte(clientECCCert), 0o777)
	os.WriteFile("client-ecc.key", []byte(clientECCKey), 0o777)
}
