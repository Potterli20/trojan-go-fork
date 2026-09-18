package router

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type MockClient struct{}

func (m *MockClient) DialConn(address *tunnel.Address, t tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, common.NewError("mockproxy")
}

func (m *MockClient) DialPacket(t tunnel.Tunnel) (tunnel.PacketConn, error) {
	return MockPacketConn{}, nil
}

func (m MockClient) Close() error {
	return nil
}

type MockPacketConn struct{}

func (m MockPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	panic("implement me")
}

func (m MockPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	panic("implement me")
}

func (m MockPacketConn) Close() error {
	panic("implement me")
}

func (m MockPacketConn) LocalAddr() net.Addr {
	panic("implement me")
}

func (m MockPacketConn) SetDeadline(t time.Time) error {
	panic("implement me")
}

func (m MockPacketConn) SetReadDeadline(t time.Time) error {
	panic("implement me")
}

func (m MockPacketConn) SetWriteDeadline(t time.Time) error {
	panic("implement me")
}

func (m MockPacketConn) WriteWithMetadata(bytes []byte, metadata *tunnel.Metadata) (int, error) {
	return 0, common.NewError("mockproxy")
}

func (m MockPacketConn) ReadWithMetadata(bytes []byte) (int, *tunnel.Metadata, error) {
	return 0, nil, common.NewError("mockproxy")
}

func TestRouter(t *testing.T) {
	data := `
router:
    enabled: true
    bypass: 
    - "regex:bypassreg(.*)"
    - "full:bypassfull"
    - "full:localhost"
    - "domain:bypass.com"
    block:
    - "regexp:blockreg(.*)"
    - "full:blockfull"
    - "domain:block.com"
    proxy:
    - "regexp:proxyreg(.*)"
    - "full:proxyfull"
    - "domain:proxy.com"
    - "cidr:192.168.1.1/16"
`
	ctx, err := config.WithYAMLConfig(context.Background(), []byte(data))
	common.Must(err)
	client, err := NewClient(ctx, &MockClient{})
	common.Must(err)
	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "proxy.com",
		Port:        80,
	}, nil)
	if err.Error() != "mockproxy" {
		t.Fatal(err)
	}
	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "proxyreg123456",
		Port:        80,
	}, nil)
	if err.Error() != "mockproxy" {
		t.Fatal(err)
	}
	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "proxyfull",
		Port:        80,
	}, nil)
	if err.Error() != "mockproxy" {
		t.Fatal(err)
	}

	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.IPv4,
		IP:          net.ParseIP("192.168.123.123"),
		Port:        80,
	}, nil)
	if err.Error() != "mockproxy" {
		t.Fatal(err)
	}

	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "block.com",
		Port:        80,
	}, nil)
	if !strings.Contains(err.Error(), "block") {
		t.Fatal("block??")
	}
	port, err := strconv.Atoi(util.HTTPPort)
	common.Must(err)

	_, err = client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "localhost",
		Port:        port,
	}, nil)
	if err != nil {
		t.Fatal("dial http failed", err)
	}

	packet, err := client.DialPacket(nil)
	common.Must(err)
	buf := [10]byte{}
	_, err = packet.WriteWithMetadata(buf[:], &tunnel.Metadata{
		Address: &tunnel.Address{
			AddressType: tunnel.DomainName,
			DomainName:  "proxyfull",
			Port:        port,
		},
	})
	if err.Error() != "mockproxy" {
		t.Fail()
	}
}

// TestRouterConcurrentRegexRegression 验证运行期对 regexCache 的并发访问不再写 map。
// 修复前:matchDomain 在首次命中某 regex 规则时会向 client.regexCache 写入,
// 而 Route() 经每连接 goroutine 并发调用,可触发 "fatal error: concurrent map writes"
// (即使不加 -race 也会崩溃)。修复后:regexCache 在 NewClient 启动阶段全部预编译,
// 运行期只读。此测试用大量 goroutine 并发命中三类(bypass/block/proxy)regex 规则。
func TestRouterConcurrentRegexRegression(t *testing.T) {
	data := `
router:
    enabled: true
    bypass:
    - "regex:bypassreg(.*)"
    block:
    - "regexp:blockreg(.*)"
    proxy:
    - "regexp:proxyreg(.*)"
`
	ctx, err := config.WithYAMLConfig(context.Background(), []byte(data))
	common.Must(err)
	client, err := NewClient(ctx, &MockClient{})
	common.Must(err)
	defer client.Close() //gosec:disable -- 测试清理路径

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 交替命中三种策略的 regex 规则。Route 只做策略判定、不实际拨号,
			// 但内部 matchDomain 会读取 regexCache —— 修复前此处会在首次命中时写 map。
			prefix := []string{"bypassreg", "blockreg", "proxyreg"}[i%3]
			domain := fmt.Sprintf("%s%d", prefix, i)
			_ = client.Route(&tunnel.Address{
				AddressType: tunnel.DomainName,
				DomainName:  domain,
				Port:        80,
			})
		}(i)
	}
	wg.Wait()
}
