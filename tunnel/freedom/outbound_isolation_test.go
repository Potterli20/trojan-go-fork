package freedom

// R18-1：拨号路径此前直接读进程级全局 getGlobalOutbound()，而后建的 freedom
// 实例在 NewClient 里 SetGlobalOutbound 覆盖全局——先建实例（router 的 bypass
// 直连客户端、热重载前的旧栈）的 outbound_local_addr/fwmark 被静默顶掉，
// 实例字段写进 Client 之后从未被读取。实例字段上的注释（outbound.go:14）早就
// 声明了这个意图，代码没兑现。
//
// API 的 SetOutboundConfig（SetGlobalOutbound）是运行期覆盖，语义是"此后所有
// 拨号都用这份值"——必须保留。所以口径是三层：override 置位 > 实例字段 > 通配。
//
// 判据全部走白盒 newDialConfig（只看装配结果，不真拨号），变异对照：把
// newDialConfig 的 effectiveOutbound 换回 getGlobalOutbound，第一条立刻转红。
import (
	"net"
	"testing"
)

func mustClient(t *testing.T, cfg *Config) *Client {
	t.Helper()
	c, err := NewClient(newCtxWithConfig(cfg), nil)
	if err != nil {
		t.Fatalf("NewClient(%+v): %v", cfg, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func dialLocalIP(t *testing.T, c *Client) net.IP {
	t.Helper()
	cfg, _, _ := c.newDialConfig("tcp", "203.0.113.1:443")
	tcpAddr, ok := cfg.LocalAddr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("LocalAddr 类型=%T，期望 *net.TCPAddr", cfg.LocalAddr)
	}
	return tcpAddr.IP
}

func TestInstanceOutboundNotOverwrittenByLaterClient(t *testing.T) {
	resetGlobalOutbound()
	t.Cleanup(resetGlobalOutbound)

	a := mustClient(t, &Config{OutboundLocalAddr: "127.0.0.2"})
	b := mustClient(t, &Config{OutboundLocalAddr: "127.0.0.3"})

	// B 后建、会写全局；A 的拨号必须仍用 A 自己的配置。
	if got := dialLocalIP(t, a); !got.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("先建实例的拨号绑定被后建实例顶掉：got %v, want 127.0.0.2", got)
	}
	if got := dialLocalIP(t, b); !got.Equal(net.ParseIP("127.0.0.3")) {
		t.Fatalf("后建实例未用自己的配置：got %v, want 127.0.0.3", got)
	}
}

func TestSetGlobalOutboundOverridesExistingInstances(t *testing.T) {
	resetGlobalOutbound()
	t.Cleanup(resetGlobalOutbound)

	a := mustClient(t, &Config{OutboundLocalAddr: "127.0.0.2"})

	// API 运行期覆盖：对所有已存在实例生效。
	SetGlobalOutbound(net.ParseIP("198.51.100.7"), 0)
	if got := dialLocalIP(t, a); !got.Equal(net.ParseIP("198.51.100.7")) {
		t.Fatalf("API 覆盖未对已存在实例生效：got %v, want 198.51.100.7", got)
	}

	// reset（清 override）后回落到实例自己的配置。
	resetGlobalOutbound()
	if got := dialLocalIP(t, a); !got.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("覆盖解除后未回落到实例配置：got %v, want 127.0.0.2", got)
	}
}

func TestNewClientAdoptsActiveOverride(t *testing.T) {
	resetGlobalOutbound()
	t.Cleanup(resetGlobalOutbound)

	SetGlobalOutbound(net.ParseIP("198.51.100.7"), 0)
	c := mustClient(t, &Config{OutboundLocalAddr: "127.0.0.2"})

	// 覆盖生效期间新建的实例也继承覆盖值，配置里的 local_addr 不参与。
	if !c.outboundLocalIP.Equal(net.ParseIP("198.51.100.7")) {
		t.Fatalf("覆盖生效期间新建实例未继承覆盖值：got %v", c.outboundLocalIP)
	}
	if got := dialLocalIP(t, c); !got.Equal(net.ParseIP("198.51.100.7")) {
		t.Fatalf("覆盖生效期间新建实例的拨号绑定错误：got %v", got)
	}
}
