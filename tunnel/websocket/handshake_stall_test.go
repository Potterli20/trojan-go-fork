package websocket

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// deadlineProbe 记录握手这一程"写方向"到底有没有被时限约束过。
// 判据不能靠时序或计数，只能直接盯住 Write 被调用的那一刻：
// 写 101 响应时若没有任何写截止时间生效，一个把请求发完就不再读取的对端
// 就能把 AcceptConn 永久钉死——而这条连接已经离开下层的待处理队列，
// 上层任何排空逻辑都够不着它。
type deadlineProbe struct {
	net.Conn
	mu            sync.Mutex
	writeDeadline time.Time
	readDeadline  time.Time
	writes        int
	boundedWrites int // 落在有效写时限内的 Write 次数
}

func (p *deadlineProbe) Metadata() *tunnel.Metadata { return nil }

func (p *deadlineProbe) SetDeadline(t time.Time) error {
	p.mu.Lock()
	p.writeDeadline, p.readDeadline = t, t
	p.mu.Unlock()
	return p.Conn.SetDeadline(t) //gosec:disable -- 测试夹具：错误由调用方断言
}

func (p *deadlineProbe) SetWriteDeadline(t time.Time) error {
	p.mu.Lock()
	p.writeDeadline = t
	p.mu.Unlock()
	return p.Conn.SetWriteDeadline(t) //gosec:disable -- 测试夹具：错误由调用方断言
}

func (p *deadlineProbe) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	p.readDeadline = t
	p.mu.Unlock()
	return p.Conn.SetReadDeadline(t) //gosec:disable -- 测试夹具：错误由调用方断言
}

func (p *deadlineProbe) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.writes++
	if !p.writeDeadline.IsZero() && p.writeDeadline.After(time.Now()) {
		p.boundedWrites++
	}
	p.mu.Unlock()
	return p.Conn.Write(b)
}

func (p *deadlineProbe) writeAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writes
}

func (p *deadlineProbe) boundedWriteWindows() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.boundedWrites
}

// deadlineStillArmed 报告移交上游时是否还残留截止时间。握手后的连接要被
// 中继长期持有，残留的时限会在几十秒后凭空掐断一条好端端的会话。
func (p *deadlineProbe) deadlineStillArmed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.writeDeadline.IsZero() || !p.readDeadline.IsZero()
}

// handoffServer 只交出一条连接的下层隧道。Close 刻意什么都不做：
// 连接一旦交给 websocket 层，下层就再没有资格、也没有句柄去关它。
type handoffServer struct{ conn tunnel.Conn }

func (s *handoffServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	c := s.conn
	s.conn = nil
	if c == nil {
		return nil, io.EOF
	}
	return c, nil
}

func (s *handoffServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (s *handoffServer) Close() error { return nil }

func upgradeRequest(path string) []byte {
	var key [16]byte
	common.Must2(rand.Read(key[:])) //gosec:disable -- 错误忽略：测试夹具
	return []byte(fmt.Sprintf("GET %s HTTP/1.1\r\n"+
		"Host: cdn.example.org\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n",
		path, base64.StdEncoding.EncodeToString(key[:])))
}

func newHandshakeServer(t *testing.T, underlay tunnel.Server) *Server {
	t.Helper()
	cfg := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: 80,
		Websocket: WebsocketConfig{
			Enabled: true,
			Host:    "cdn.example.org",
			Path:    "/ws",
		},
	}
	s, err := NewServer(config.WithConfig(context.Background(), Name, cfg), underlay)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

// waitUntil 有界轮询，命中返回 true。
func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestStalledPeerDoesNotPinAcceptConn 复现"对端读完握手请求就停手"的形态。
// 真实网络上它叫 TCP 零窗口：一个不鉴权的对端发完 GET 升级请求后把接收窗置 0，
// 我们写 101 响应就永远阻塞。AcceptConn 是被入站的 accept 循环串行调用的，
// 一条卡死 = 这个入站从此不再接纳任何新连接；Server.Close 也必须能把它叫醒。
func TestStalledPeerDoesNotPinAcceptConn(t *testing.T) {
	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	probe := &deadlineProbe{Conn: raw}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})

	go peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：对端读完即停手

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = srv.AcceptConn(&Tunnel{})
	}()

	if !waitUntil(5*time.Second, func() bool { return probe.writeAttempts() > 0 }) {
		t.Fatal("用例没有走到写 101 响应这一步，判据失效（对端没有被读到请求？）")
	}
	// 此刻对端不再读取任何一个字节，AcceptConn 应当正阻塞在这次写上
	start := time.Now()
	if err := srv.Close(); err != nil {
		t.Logf("Close 返回（不影响判据）：%v", err)
	}
	select {
	case <-done:
		t.Logf("Close 在 %v 内叫醒了阻塞在握手响应上的 AcceptConn", time.Since(start).Round(time.Millisecond))
	case <-time.After(3 * time.Second):
		t.Fatalf("Close 之后 %v AcceptConn 仍卡在写 101 响应上：整条 accept 循环被一个不鉴权的对端钉死，"+
			"这条连接的 fd 与握手状态也无人回收", 3*time.Second)
	}
}

// TestHandshakeWriteWindowIsBounded 要求写握手响应的那一程本身是有界的：
// 对端正常读取时握手能顺利完成，但时限必须真的挂上（否则慢对端可以无限拖），
// 并且移交上游前必须解除（否则长期使用会在时限到点时被凭空掐断）。
func TestHandshakeWriteWindowIsBounded(t *testing.T) {
	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	probe := &deadlineProbe{Conn: raw}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})

	// 对端：发完请求后读走 101，随后一直读到会话结束
	resp := make(chan int, 1)
	stopPeer := make(chan struct{})
	go func() {
		peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：测试夹具
		buf := make([]byte, 256)
		n, _ := peer.Read(buf)
		resp <- n
		<-stopPeer
		peer.Close() //gosec:disable -- 错误忽略：测试清理
	}()

	conn, err := srv.AcceptConn(&Tunnel{})
	if err != nil {
		t.Fatalf("正常对端的握手应当成功：%v", err)
	}
	if n := <-resp; n <= 0 {
		t.Fatalf("对端没有读到 101 响应（读到 %d 字节）", n)
	}
	bounded := probe.boundedWriteWindows()
	armed := probe.deadlineStillArmed()

	// 先让对端撤掉，再关会话：coder 的 Close 会发 close 帧并等对端回执，
	// 哑对端不回执就要白等满库内的写超时
	if conn != nil {
		close(stopPeer)
		conn.Close() //gosec:disable -- 错误忽略：测试清理
	}
	srv.Close() //gosec:disable -- 错误忽略：测试清理

	if bounded == 0 {
		t.Fatalf("写 101 响应时一次写时限都没有：%d 次写入全部裸奔，"+
			"零窗口对端可以让 AcceptConn 永久阻塞", probe.writeAttempts())
	}
	if armed {
		t.Fatal("握手结束后没有解除截止时间：这条连接会被握手时限凭空掐断")
	}
}
