package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

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
	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithInsecure())
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
	for i := 0; i < 3; i++ {
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

func TestTLS(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	cfg := &Config{
		API: APIConfig{
			Enabled: true,
			APIHost: "127.0.0.1",
			APIPort: port,
			SSL: SSLConfig{
				Enabled:        true,
				CertPath:       "server.crt",
				KeyPath:        "server.key",
				VerifyClient:   false,
				ClientCertPath: []string{"client.crt"},
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
	certBytes, err := ioutil.ReadFile("server.crt")
	common.Must(err)
	pool.AppendCertsFromPEM(certBytes)

	certificate, err := tls.LoadX509KeyPair("client.crt", "client.key")
	common.Must(err)
	creds := credentials.NewTLS(&tls.Config{
		ServerName:   "localhost",
		RootCAs:      pool,
		Certificates: []tls.Certificate{certificate},
	})
	conn, err := grpc.Dial(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(creds))
	common.Must(err)
	server := NewTrojanServerServiceClient(conn)
	stream, err := server.ListUsers(ctx, &ListUsersRequest{})
	common.Must(err)
	stream.CloseSend()
	conn.Close()
}

var serverCert = `
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

var serverKey = `
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

var clientKey = `
-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDSuMxahZt4QeSn
CwDbi0J2rGiI68IJn905TjgVOeZigt1b80CZI59/g4hTV2eOcPxF+rtU29EFvrcK
kTsaBVDSqBOXhFmKP90Mt12NpTzSX+XrxptKJ7ZovE+Bwghr4JP/3elkTUZnszhu
msIBVmem7RHzrFw5WKZKVHdbPwFQg2rWY7Ultvf8KEAO0jB7tadKGHQf0ULvY6XT
NXv3zvnMN/lRSpa25FPKCjLdliTo6kQ/39lO0R2iX/FSX0CR52chZ/mLLLaUiPts
CaI7GvdaHuPtaV8Vt05Kk4BzDTCJha/Sx0v+76JT/KNUk7XTS0K8S1COrgyYMckk
4xY71roPAgMBAAECggEAKEWTIJW6QcBuH5KVxl+WAzIuBETyX36C/Am75CqdoiQa
hBE4Pkw1llwf+LWSoAFt5T3nAW/FQdDSEJ3y6qUrbicbH3D239oWt/BvW7vBpP8Y
5PefBAwU621Z0JWxoFRaVKMnkLjIBNeWqGTBQRovUxKpxKUjNv5/QWNlMDYZXTZ3
VvNObhm4WZfBLpt1M9TapIG1aL0rm8W4b83AAmZRF+mHI8mScfa0Qn2YHv3cGDS8
SunSwtaj3MUYOvS9hkpdkvhSDw5BBZbblZ4L5mzeAy3NjzmI6/lRvGqxmhRMVxQJ
Ff50RlncAt2dsyd5J9d9QORpzoFJAcknuBsLnWNgcQKBgQD+U3E+6riRCbD6SqnX
h8R32O0UU+MVwSNA6VnMAv3B//91EB2iviwR27cIa40HW/e7GGUZeKDmEgu6/t0A
eNIpOCtyDFX6j90yDvFmO/yZve9McNNRrhPl8ox3OQVjKtPVt7PHuSlfFr+ox7Vi
5suZT5dqNo3WRoJYKr8wg3KWlwKBgQDUG+E+i8Qe+X6tFB9wnZCz9kMGJDctjLwg
3GfH6Gyq966c4JYREOAw5rQ9N49GrDkFJSWlw2WHRJpWvdqpAuilfVxkrhE1jW6B
rIdUsqPisDMXmTgsJ8LZUPeap9oXfrHZPykiwvDe7fufj1vF3fQKTFq65HBDAqTX
8a7o1q6fSQKBgHNlGPUbOzNT2mE2j2mjyJk4bBnVFixAveYt+vh+QvVLWnWbIlc9
QnG354yCgDLen8DciMLN4PODLJ9kFJKqP3FEczIENt7Bd/PGo/FnNm3rqDBe4QMm
oRCsxN8zmCYuwH8wIvp0ITlr8Pp74ulFHwwo+OLQbfrTc0Dd5HH2sn9xAoGAYDAk
fVC0p7dNEwBFIbTSoknTKz3RJ/7icaSCC84DOaUIsmvGogadJI/6vKgteUcwtHyc
DggGSsl5lEyUlICVMDchZybo9vgkXPn4hRhd6bct9E2vg5akbhihsKjd5jm2PWa/
KNxujyotKbbBT4HP4buuiYJ+xmS0jJk1ULHKjsECgYEAhIkifdPPEHvPeaDlL1kv
aLsfDNi4kSpvLltJkL6DdZSCVIuEK01vzjhEijhm2HyfK8XVY+VULAzWxTv1LmMq
dseSruHdwl2xsm7NLM3XKIjaVIeoRiHyzcYffXVMPxaPZE894z8eSzziuzyfIvyp
kDzS1fnKn+jNfFEsew02CbE=
-----END PRIVATE KEY-----
`

var clientCert = `
-----BEGIN CERTIFICATE-----
MIIDUTCCAjmgAwIBAgIQQaKDB58uncpXiosDdR9roTANBgkqhkiG9w0BAQsFADAR
MQ8wDQYDVQQDDAZmcmVnaWUwHhcNMjExMDE1MDI1MzMwWhcNMjQwOTI5MDI1MzMw
WjAYMRYwFAYDVQQDDA10cm9qYW4tY2xpZW50MIIBIjANBgkqhkiG9w0BAQEFAAOC
AQ8AMIIBCgKCAQEA0rjMWoWbeEHkpwsA24tCdqxoiOvCCZ/dOU44FTnmYoLdW/NA
mSOff4OIU1dnjnD8Rfq7VNvRBb63CpE7GgVQ0qgTl4RZij/dDLddjaU80l/l68ab
Sie2aLxPgcIIa+CT/93pZE1GZ7M4bprCAVZnpu0R86xcOVimSlR3Wz8BUINq1mO1
Jbb3/ChADtIwe7WnShh0H9FC72Ol0zV79875zDf5UUqWtuRTygoy3ZYk6OpEP9/Z
TtEdol/xUl9AkednIWf5iyy2lIj7bAmiOxr3Wh7j7WlfFbdOSpOAcw0wiYWv0sdL
/u+iU/yjVJO100tCvEtQjq4MmDHJJOMWO9a6DwIDAQABo4GdMIGaMAkGA1UdEwQC
MAAwHQYDVR0OBBYEFJfmZL8JUPdWl6fWWeL+owxiAyp4MEwGA1UdIwRFMEOAFMkl
04nz1F3a76mVLaKRZN3UkBy7oRWkEzARMQ8wDQYDVQQDDAZmcmVnaWWCFESDK1xM
TlAkJS4f2hhyHJuhP6RcMBMGA1UdJQQMMAoGCCsGAQUFBwMCMAsGA1UdDwQEAwIH
gDANBgkqhkiG9w0BAQsFAAOCAQEAeGy0Dq639QQEiqs1G0yawrOE1MS8fHhpbH70
JtZRnWJqWGGdmxU+EFhEE6ptBCBxUCKnq2EDileb33txAkIa+bMxGhwLRR/QBkuc
F+8gvn7OlOc2d2PgJnobAGwdzms37oAUwYtWTo3G7AjFAgRXVALNwvY3s1mPhnO6
RO1G2GdJuse3KlW61wvXQIBOPkXBPZ9AgV7mXq94iGAhdV/GToYGQAPLvXlyZFXC
Qxx4GvAtMjIi/YVnwoLtLLq/k5uCt2AOc1Wss41RKRgf8uqXITF+RePb6VRjuPeZ
OhJo6IZuhOPHL/xzbaDEzkXur3dJRG1CsVaP/W9AR5kNNHWPqw==
-----END CERTIFICATE-----
`

func init() {
	ioutil.WriteFile("server.crt", []byte(serverCert), 0o777)
	ioutil.WriteFile("server.key", []byte(serverKey), 0o777)
	ioutil.WriteFile("client.crt", []byte(clientCert), 0o777)
	ioutil.WriteFile("client.key", []byte(clientKey), 0o777)
}
