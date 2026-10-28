package trojan

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// quietConn 只实现这条判据用得上的方法：Write 立刻整量成功，于是耗时全部落在限速排队上；
// Close 不碰真实 fd，RemoteAddr 要给出来——InboundConn.Close 的日志实参是先行求值的。
type quietConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *quietConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *quietConn) Read([]byte) (int, error)    { return 0, net.ErrClosed }
func (c *quietConn) Close() error                { c.closed.Store(true); return nil }
func (c *quietConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4242}
}

// pacedUser 造一个真实的 memory.User 并压上低速限额——判据要量的是"拆会话能不能叫醒排队"，
// 用真实现而不是替身，替身会把接线本身测没。
func pacedUser(t *testing.T, limit int) (*memory.User, func()) {
	t.Helper()
	cfg := &memory.Config{Passwords: []string{"paced-session-password"}}
	auth, err := memory.NewAuthenticator(config.WithConfig(context.Background(), memory.Name, cfg))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	users := auth.ListUsers()
	if len(users) != 1 {
		auth.Close()
		t.Fatalf("期望 1 个用户，实得 %d", len(users))
	}
	u, ok := users[0].(*memory.User) // SetSpeedLimit 不在 statistic.User 上，这里要具体类型
	if !ok {
		auth.Close()
		t.Fatalf("ListUsers 没有返回 *memory.User：%T", users[0])
	}
	u.SetSpeedLimit(limit, limit)
	return u, func() { auth.Close() }
}

func newPacedInbound(u *memory.User) (*InboundConn, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	return &InboundConn{
		Conn:     &quietConn{},
		user:     u,
		hash:     "0123456789abcdef0123456789abcdef0123456789abcdef0123",
		ip:       "127.0.0.1",
		ipX:      "127.0.0.1",
		metadata: &tunnel.Metadata{Address: &tunnel.Address{DomainName: "example.com", Port: 80, AddressType: tunnel.DomainName}},
		ctx:      ctx,
		cancel:   cancel,
	}, ctx
}

// TestSessionCloseWakesPacedWrite 钉住 trojan 这一侧的接线：会话 ctx 必须真的交给限速排队，
// 而 Close 必须取消它。少接一头，被限速的用户在 relay 拆会话之后就还要按 n/limit 排队
// （这里 8192 字节 @1000 B/s ≈ 6.2s），期间它攥着从 proxy 池里借走的中继 buffer——
// Proxy.Close 的 5s 预算只是放弃等待，叫不醒这条协程。
//
// 这条判据跑在真实墙上时钟上，但两个读数差一个数量级（~6.2s 对比"Close 之后立即返回"），
// 阈值取 2s 不会因机器负载翻转；限速语义本身的精确读数由 synctest 判据负责
// （statistic/memory/limiter_stop_test.go）。
func TestSessionCloseWakesPacedWrite(t *testing.T) {
	u, release := pacedUser(t, 1000)
	t.Cleanup(release)
	conn, sessionCtx := newPacedInbound(u)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		conn.Write(make([]byte, 8192))
		done <- time.Since(start)
	}()

	time.Sleep(300 * time.Millisecond) // 让 Write 确实进入排队
	closeStart := time.Now()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case d := <-done:
		if d > 2*time.Second {
			t.Fatalf("拆会话之后这次 Write 又等了 %v 才返回（本次需要排队约 6.2s）："+
				"会话 ctx 没有并进限速排队，关停叫不醒它", d)
		}
		if spent := time.Since(closeStart); spent > time.Second {
			t.Fatalf("Close 之后过了 %v 才被限速叫醒：接线没生效", spent)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("拆会话之后 Write 再也没有返回：这条协程连同借走的中继 buffer 永久留在池外")
	}

	if sessionCtx.Err() == nil {
		t.Fatal("Close 没有取消会话 ctx：限速排队与所有下游都少了这个唤醒信号")
	}
	// 等待被打断不等于流量没发生——账目必须照记，否则 quota 与统计系统性偏低。
	if sent, _ := u.GetTraffic(); sent < 8192 {
		t.Fatalf("被中断的限速等待丢了账目：sent=%d 期望至少 8192", sent)
	}
}

// TestInboundConnCloseIsIdempotentForSessionCtx 是收尾成对性的下限检查：trojan 的 UDP 会话
// 里 packet.go 会先 Close 一次、relay 的 defer 再 Close 一次，cancel 只能被调用一次，
// 而两次都必须照常释放底层连接。
func TestInboundConnCloseIsIdempotentForSessionCtx(t *testing.T) {
	u, release := pacedUser(t, 0) // 不限速：这条判据不排队
	t.Cleanup(release)
	conn, sessionCtx := newPacedInbound(u)

	if err := conn.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("第二次 Close: %v", err)
	}
	if sessionCtx.Err() == nil {
		t.Fatal("会话 ctx 没被取消")
	}
	qc := conn.Conn.(*quietConn)
	if !qc.closed.Load() {
		t.Fatal("底层连接没被关闭")
	}
	// IP 槽按调用次数递减，多减一次会提前释放槽位、绕过 MaxIPNum（见结构体上的注释）。
	if n := u.GetIP(); n != 0 {
		t.Fatalf("重复 Close 之后 IP 计数为 %d，期望 0", n)
	}
}
