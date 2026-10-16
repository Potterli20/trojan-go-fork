package socks

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// srcPacket 是"外网来的一个包 + 它的源地址"。socks 的会话按源地址分组，上限判据要
// 每个包来自不同源，所以不能沿用 udp_session_leak_test.go 里那个固定 src 的夹具。
type srcPacket struct {
	payload []byte
	src     net.Addr
}

// multiSrcListenConn 只提供 packetDispatchLoop 与会话守护协程用得上的方法。
// errAlways 非空时 ReadFrom 一律失败，用来测错误退避。
type multiSrcListenConn struct {
	feed      chan srcPacket
	errAlways error
	calls     counters
	writes    counters
	closed    chan struct{}
	once      sync.Once
}

type counters struct {
	mu sync.Mutex
	n  int
}

func (c *counters) bump() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *counters) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (m *multiSrcListenConn) ReadFrom(p []byte) (int, net.Addr, error) {
	m.calls.bump()
	if m.errAlways != nil {
		return 0, nil, m.errAlways
	}
	select {
	case pkt := <-m.feed:
		return copy(p, pkt.payload), pkt.src, nil
	case <-m.closed:
		return 0, nil, net.ErrClosed
	}
}

func (m *multiSrcListenConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	m.writes.bump()
	return len(p), nil
}
func (m *multiSrcListenConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (m *multiSrcListenConn) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (m *multiSrcListenConn) SetDeadline(time.Time) error      { return nil }
func (m *multiSrcListenConn) SetReadDeadline(time.Time) error  { return nil }
func (m *multiSrcListenConn) SetWriteDeadline(time.Time) error { return nil }

func (m *multiSrcListenConn) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}

// 下面两个方法属于 tunnel.PacketConn 接口，中继实际用的是会话自己的实现，不会被调到。
func (m *multiSrcListenConn) WriteWithMetadata([]byte, *tunnel.Metadata) (int, error) {
	return 0, errors.New("not used by socks dispatch")
}

func (m *multiSrcListenConn) ReadWithMetadata([]byte) (int, *tunnel.Metadata, error) {
	return 0, nil, errors.New("not used by socks dispatch")
}

// newSeededServer 造一个 mapping 已灌到 seed 条的 Server，返回会话表与 seed+1 个
// 互不相同的源地址（最后一个是"越界的那条"）。
func newSeededServer(ctx context.Context, conn *multiSrcListenConn, seed int) (*Server, *PacketConn, *net.UDPAddr) {
	s := &Server{
		listenPacketConn: conn,
		packetChan:       make(chan tunnel.PacketConn, 4),
		mapping:          make(map[string]*PacketConn, maxUDPSessions+1),
		timeout:          5 * time.Minute,
		ctx:              ctx,
	}
	var first *PacketConn
	var overCap *net.UDPAddr
	for i := 0; i <= seed; i++ {
		src := &net.UDPAddr{IP: net.IPv4(10, 0, byte(i>>16), byte(i>>8)), Port: 2000 + i}
		if i < seed {
			pc := &PacketConn{
				input:  make(chan *packetInfo, udpSessionQueueSize),
				output: make(chan *packetInfo, udpSessionQueueSize),
				ctx:    ctx,
				cancel: func() {},
				src:    src,
			}
			s.mapping[src.String()] = pc
			if i == 0 {
				first = pc
			}
		} else {
			overCap = src
		}
	}
	return s, first, overCap
}

func feed(conn *multiSrcListenConn, src net.Addr, payload string) {
	// socks 的 dispatch 会按 SOCKS5 UDP 格式拆帧（RSV+FRAG+ATYP+地址+端口），
	// 所以"外网来的包"必须是合法帧，否则测的就不是会话表而是拆帧闸门了。
	conn.feed <- srcPacket{payload: socksPacket(payload), src: src}
}

// TestUDPSessionCountIsBounded 钉住会话表上限：mapping 原本只有空闲超时，条目数等于
// UDPTimeout × 新源地址到达速率，而源地址可以伪造——每条会话攥着两条深度 16 的通道、
// 一个子 ctx 和一个守护协程。闸门必须卡在**造会话对象之前**。
// 口径同 tunnel/tproxy/session_bound_test.go（那一层先补的）。
func TestUDPSessionCountIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &multiSrcListenConn{feed: make(chan srcPacket, 4), closed: make(chan struct{})}
	s, existing, overCap := newSeededServer(ctx, conn, maxUDPSessions)

	go s.packetDispatchLoop()

	feed(conn, overCap, "x")
	// 同步点：投给一条**已存在**的会话。dispatch 是单消费者，这条被塞进 input 就说明
	// 上一条已经判完；表满时新源地址一律开不出会话，所以不能拿"新建一条"当同步点。
	feed(conn, existing.src, "y")
	select {
	case info := <-existing.input:
		if len(info.payload) != 1 {
			t.Fatalf("同步点载荷不对: %q", info.payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch 循环没有处理投递的包，判据失效")
	}

	s.mappingLock.RLock()
	_, hasOverCap := s.mapping[overCap.String()]
	size := len(s.mapping)
	s.mappingLock.RUnlock()

	if hasOverCap {
		t.Fatalf("会话表已有 %d 条时仍为新源地址开了会话：条目数由包量决定，没有上限", maxUDPSessions)
	}
	if size != maxUDPSessions {
		t.Fatalf("会话表 %d 条，上限是 %d", size, maxUDPSessions)
	}
}

// TestUDPSocketErrorBacksOff 钉住读错误退避。旧写法 `if err != nil { …; continue }`
// 不看错误性质：socket 一旦进入持续报错状态，这就是满 CPU 的热循环，每轮还打一条日志。
func TestUDPSocketErrorBacksOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &multiSrcListenConn{
		closed:    make(chan struct{}),
		errAlways: errors.New("socks test: persistent read failure"),
	}
	s, _, _ := newSeededServer(ctx, conn, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.packetDispatchLoop()
	}()

	time.Sleep(400 * time.Millisecond)
	calls := conn.calls.get()
	if calls > 30 {
		t.Fatalf("持续读错误时 400ms 内读了 %d 次：没有退避，这是一条满 CPU 的热循环", calls)
	}
	if calls < 2 {
		t.Fatalf("400ms 只读了 %d 次：退避过头，socket 恢复后 UDP 会长时间无人取包", calls)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消 ctx 之后读循环没有退出：退避把关停也一起拖住了")
	}
}

// TestUDPServerDropsShortFrames 钉住拆帧闸门。`bytes.NewReader(buf[3:n])` 在 n<3 时
// 是切片越界 panic，而这里的对端是**未认证的 UDP 源**：一个零长度数据报就能把整个进程
// 打崩（relay 这条链上没有 recover，只有 mux/tls/trojan 三处有）。闸门还必须卡在
// 建会话之前，否则 n=3 这种"不 panic 但也投递不出负载"的包会先攒出一条死会话。
func TestUDPServerDropsShortFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &multiSrcListenConn{feed: make(chan srcPacket, 8), closed: make(chan struct{})}
	s, _, _ := newSeededServer(ctx, conn, 0)
	go s.packetDispatchLoop()

	zombie := &net.UDPAddr{IP: net.IPv4(10, 9, 9, 9), Port: 41000}
	for _, short := range [][]byte{nil, {0}, {0, 0}, {0, 0, 0}} {
		conn.feed <- srcPacket{payload: short, src: zombie}
	}

	// 闸门之后的合法包仍要正常建会话并投递，说明这条路径没被一并堵死
	conn.feed <- srcPacket{payload: socksPacket("ping"), src: zombie}
	accepted, err := waitAccepted(s)
	if err != nil {
		t.Fatalf("合法包没有建出会话: %v", err)
	}
	session := accepted.(*PacketConn)
	info, err := waitInput(session)
	if err != nil {
		t.Fatalf("合法包的负载没有投递出来: %v", err)
	}
	if string(info.payload) != "ping" {
		t.Fatalf("负载不对: %q", info.payload)
	}

	s.mappingLock.RLock()
	sessions := 0
	for key := range s.mapping {
		if key == zombie.String() {
			sessions++
		}
	}
	s.mappingLock.RUnlock()
	if sessions != 1 {
		t.Fatalf("同一个源地址在表里有 %d 条会话：短包在建会话之前就该被丢掉", sessions)
	}
}

func waitAccepted(s *Server) (tunnel.PacketConn, error) {
	select {
	case pc := <-s.packetChan:
		return pc, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("packetChan 超时")
	}
}

func waitInput(session *PacketConn) (*packetInfo, error) {
	select {
	case info := <-session.input:
		return info, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("input 超时")
	}
}
