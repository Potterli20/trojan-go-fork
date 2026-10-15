package mux

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// immediateUnderlay 每次拨号立刻产一条全新的本地管道连接，让 DialConn 在丢弃死会话后
// 能正常走出「拨新会话」这条路。
type immediateUnderlay struct{ t *testing.T }

func (u immediateUnderlay) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	a, b := net.Pipe()
	// 对端持续收取，否则 smux 的写循环会阻塞在管道上
	go io.Copy(io.Discard, b) //gosec:disable -- 错误忽略：测试清理
	u.t.Cleanup(func() {
		a.Close() //gosec:disable -- 错误忽略：测试清理
		b.Close() //gosec:disable -- 错误忽略：测试清理
	})
	return &trackedTunnelConn{Conn: a}, nil
}

func (u immediateUnderlay) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported")
}

func (u immediateUnderlay) Close() error { return nil }

// deadSession 造一个「已死但还留在池里」的 mux 会话。
// registered 是登记进池子、代表 underlayConn 的那条独立连接：真实代码里它和 smux
// 持有的裸连接是同一个对象，这里分开是为了让判据只落在「丢弃路径有没有调用它的
// Close」上，不被 session.Close() 顺带关掉裸连接这件事污染。
func deadSession(t *testing.T) (*smux.Session, *trackedTunnelConn) {
	t.Helper()
	rawA, rawB := net.Pipe()
	go io.Copy(io.Discard, rawB) //gosec:disable -- 错误忽略：测试清理
	session, err := smux.Client(rawA, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("smux.Client: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if !session.IsClosed() {
		t.Fatal("构造的会话不是已关闭状态")
	}
	registered := &trackedTunnelConn{Conn: rawA}
	t.Cleanup(func() {
		rawA.Close() //gosec:disable -- 错误忽略：测试清理
		rawB.Close() //gosec:disable -- 错误忽略：测试清理
	})
	return session, registered
}

// TestDialConnReclaimsDroppedDeadSession 钉住 DialConn 第一遍扫池的丢弃分支。
//
// 旧写法 `info.client.IsClosed() -> delete + continue` 只把条目从池里摘掉就走了，
// 而 cleanLoop 对同一种死会话是 client.Close() + underlayConn.Close() 双关。
// 差别在于：条目一旦离开池，cleanLoop 和 Client.Close 都再也看不见它，
// 底层那条 trojan/TLS TCP 连接（以及 smux 的 readLoop/writeLoop、stickyConn 的
// padding 语义）就没人负责回收了。窗口是「会话在两次巡检之间死掉、又恰好先来一次
// DialConn」，每命中一次漏一整条连接加一个 fd。
func TestDialConnReclaimsDroppedDeadSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	session, registered := deadSession(t)

	c := &Client{
		clientPool:  make(map[muxID]*smuxClientInfo),
		underlay:    immediateUnderlay{t: t},
		concurrency: 4,
		timeout:     time.Minute,
		ctx:         ctx,
		cancel:      cancel,
	}
	info := &smuxClientInfo{id: muxID(0xdeadbeef), client: session, underlayConn: registered}
	info.lastActiveTime.Store(time.Now().UnixNano())
	c.clientPoolLock.Lock()
	c.clientPool[info.id] = info
	c.clientPoolLock.Unlock()

	conn, err := c.DialConn(nil, &Tunnel{})
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	// 池里此刻只剩「刚拨出来的新会话」，所以判据要盯死那条被丢弃的条目本身
	c.clientPoolLock.Lock()
	_, stillPooled := c.clientPool[info.id]
	c.clientPoolLock.Unlock()
	if stillPooled {
		t.Fatal("死会话没有从池里摘掉")
	}
	if !registered.closed.Load() {
		t.Fatal("DialConn 丢弃死会话时没有关闭 underlayConn：条目已离开池，" +
			"此后 cleanLoop 与 Client.Close 都再也回收不到这条底层连接")
	}
}

// TestDialConnReclaimsDeadSessionsEvenWhenOneIsUsable 覆盖更贴近真实的一侧：
// 池里同时有死会话和可用会话时，旧代码在命中可用会话的那一刻就直接 return，
// 之前已经被 delete 的死条目同样没人关。
func TestDialConnReclaimsDeadSessionsEvenWhenOneIsUsable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	deadSessionObj, registered := deadSession(t)

	liveRaw, livePeer := net.Pipe()
	go io.Copy(io.Discard, livePeer) //gosec:disable -- 错误忽略：测试清理
	live, err := smux.Client(liveRaw, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("smux.Client(live): %v", err)
	}
	t.Cleanup(func() {
		live.Close() //gosec:disable -- 错误忽略：测试清理
		liveRaw.Close()
		livePeer.Close()
	})

	c := &Client{
		clientPool:  make(map[muxID]*smuxClientInfo),
		underlay:    immediateUnderlay{t: t},
		concurrency: 4,
		timeout:     time.Minute,
		ctx:         ctx,
		cancel:      cancel,
	}
	dead := &smuxClientInfo{id: muxID(0x1), client: deadSessionObj, underlayConn: registered}
	dead.lastActiveTime.Store(time.Now().UnixNano())
	liveInfo := &smuxClientInfo{
		id:           muxID(0x2),
		client:       live,
		underlayConn: &trackedTunnelConn{Conn: liveRaw},
	}
	liveInfo.lastActiveTime.Store(time.Now().UnixNano())
	c.clientPoolLock.Lock()
	c.clientPool[dead.id] = dead
	c.clientPool[liveInfo.id] = liveInfo
	c.clientPoolLock.Unlock()

	// 反复拨号：map 遍历顺序随机，只要死条目先被扫到，旧代码就会在返回前把它丢在半路
	for i := 0; i < 8; i++ {
		conn, err := c.DialConn(nil, &Tunnel{})
		if err != nil {
			t.Fatalf("DialConn #%d: %v", i, err)
		}
		conn.Close() //gosec:disable -- 错误忽略：测试清理
		if registered.closed.Load() {
			return
		}
	}
	t.Fatal("死会话的 underlayConn 始终没被关闭：可用会话命中时，先被 delete 的死条目被丢在池外无人回收")
}
