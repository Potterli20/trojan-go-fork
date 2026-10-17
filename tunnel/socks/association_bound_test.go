package socks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// stubAssocConn 是一条"建好关联就发呆"的控制连接：Read 一直阻塞直到 Close，
// 这正是 maxAssociations 那道闸要拦住的对端形态。
type stubAssocConn struct {
	closed atomic.Bool
	done   chan struct{}
}

func newStubAssocConn() *stubAssocConn {
	return &stubAssocConn{done: make(chan struct{})}
}

func (c *stubAssocConn) Read([]byte) (int, error) {
	<-c.done
	return 0, io.EOF
}

func (c *stubAssocConn) Write(p []byte) (int, error) { return len(p), nil }

func (c *stubAssocConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.done)
	}
	return nil
}

func (c *stubAssocConn) LocalAddr() net.Addr { return nil }
func (c *stubAssocConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1).To4(), Port: 1080}
}
func (c *stubAssocConn) SetDeadline(time.Time) error      { return nil }
func (c *stubAssocConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubAssocConn) SetWriteDeadline(time.Time) error { return nil }
func (c *stubAssocConn) Metadata() *tunnel.Metadata       { return nil }

// TestAssociationCountIsBounded 钉住 associations 的条目上限。这张表登记的是已握手的
// ASSOCIATE 控制连接，过去只有 associationMaxLife(10min) 一个时限：条目数 = 发呆对端的
// 到达速率 × 600s，而这条 TCP 在握手阶段没有任何认证，每条登记攥着 1 个 fd、1 个守护
// 协程、1 个 AfterFunc 计时器和 1 个 map 条目。同层的 mapping 早就有 maxUDPSessions，
// 这里是「新增同类表照这个形态抄」那条不变量漏掉的姊妹表。
// 判据形态同 TestUDPSessionCountIsBounded：闸门卡在登记之前，且拒绝时不留残留。
func TestAssociationCountIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Server{
		ctx:          ctx,
		associations: make(map[tunnel.Conn]struct{}, maxAssociations+1),
	}
	for i := 0; i < maxAssociations; i++ {
		s.associations[newStubAssocConn()] = struct{}{}
	}

	refused := newStubAssocConn()
	if err := s.watchAssociation(refused); err == nil {
		t.Fatalf("在途关联已有 %d 条时仍接下了新的 ASSOCIATE：条目数由对端速率决定，没有上限", maxAssociations)
	}
	s.assocLock.Lock()
	size := len(s.associations)
	_, hasRefused := s.associations[refused]
	s.assocLock.Unlock()
	if hasRefused {
		t.Fatal("被拒绝的连接仍然登记在 associations 里")
	}
	if size != maxAssociations {
		t.Fatalf("在途关联 %d 条，上限是 %d", size, maxAssociations)
	}
	if refused.closed.Load() {
		t.Fatal("watchAssociation 关掉了它没有接管的连接：fd 要由调用方回完失败帧再回收")
	}

	// 上限以下必须照常接管，并且连接结束时自己注销——否则这道闸只是把泄露换成了拒绝
	s.assocLock.Lock()
	for seeded := range s.associations {
		delete(s.associations, seeded)
		break
	}
	s.assocLock.Unlock()

	taken := newStubAssocConn()
	if err := s.watchAssociation(taken); err != nil {
		t.Fatalf("未到上限时接管失败: %v", err)
	}
	if err := waitAssociationCount(s, maxAssociations); err != nil {
		t.Fatalf("接管后没有登记: %v", err)
	}
	_ = taken.Close()
	if err := waitAssociationCount(s, maxAssociations-1); err != nil {
		t.Fatalf("控制连接结束后守护协程没有注销登记: %v", err)
	}
	s.wg.Wait()
}

// TestWatchAssociationRefusesAfterClose 钉住关停竞争：Close 收完登记表之后不能再往里塞，
// 否则那条 fd 谁都够不着，只能等进程退出兜底。
func TestWatchAssociationRefusesAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, associations: make(map[tunnel.Conn]struct{})}
	linger := newStubAssocConn()
	if err := s.watchAssociation(linger); err != nil {
		t.Fatalf("基线接管失败，判据失效: %v", err)
	}
	cancel()
	s.closeAssociations()
	_ = linger.Close() // 让守护协程从阻塞读里出来（Close 已经关过一次，幂等）

	late := newStubAssocConn()
	if err := s.watchAssociation(late); err == nil {
		t.Fatal("Close 之后仍然接受了新的关联")
	}
	s.assocLock.Lock()
	size := len(s.associations)
	s.assocLock.Unlock()
	if size != 0 {
		t.Fatalf("关停后的登记表里还剩 %d 条，这些 fd 没人会关", size)
	}
	s.wg.Wait()
}

// TestDeclineAssociateGoldenBytes 钉住被容量闸拒掉时回给客户端的帧。
// 标准 socks5 客户端按定长 10 字节解析 VER/REP/RSV/ATYP + BND.ADDR(4) + BND.PORT(2)，
// 只回两字节再断线会被读成协议错误；REP=0x01 才是"服务器拒绝"这个明确信号。
// 手写字面量而不是"再算一遍"，口径同 TestAssociateReplyGoldenBytes。
func TestDeclineAssociateGoldenBytes(t *testing.T) {
	rec := &writeRecorder{}
	declineAssociate(rec)
	want := []byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	got := rec.buf.Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("ASSOCIATE 拒绝响应不对\n got % x (%d 字节)\nwant % x", got, len(got), want)
	}
}

func waitAssociationCount(s *Server, want int) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.assocLock.Lock()
		got := len(s.associations)
		s.assocLock.Unlock()
		if got == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.assocLock.Lock()
	got := len(s.associations)
	s.assocLock.Unlock()
	return fmt.Errorf("在途关联停在 %d 条，期望 %d", got, want)
}
