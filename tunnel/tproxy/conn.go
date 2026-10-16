//go:build linux

package tproxy

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Conn struct {
	net.Conn
	metadata *tunnel.Metadata
}

func (c *Conn) Metadata() *tunnel.Metadata {
	return c.metadata
}

type packetInfo struct {
	metadata *tunnel.Metadata
	payload  []byte
}

// packetBufPool 复用 UDP 负载缓冲。此前这条链上每包各 make 一次：
// 读协程 `payload := make([]byte, n)`、`WriteWithMetadata` 的 `newP := make(len p)`——
// 分配次数与包数同阶，而队列深度 16、负载最大 8KB 的驻留本来就已经按池的形状设计好了。
// 借用权随 packetInfo 交给消费端（守护协程 / ReadWithMetadata），用完归还；
// 会话被拆时 `Close` 排空队列并逐个归还——这正是 router 那轮补过的不变量
// （见 AGENTS.md「拆会话时把队列里借出的 buffer 归还进池」）。
//
// outstanding 是判据用的计数：Get 加、Put 减。判据形态见 tproxy/pool_account_test.go：
// 一轮突发之后关掉会话，必须归零；为负说明有 buffer 被归还了两次（那比泄露严重，
// 两个包会共享同一块底层数组、互相串数据）。
var (
	packetBufPool = sync.Pool{
		New: func() any {
			buf := make([]byte, MaxPacketSize)
			return &buf
		},
	}
	outstanding atomic.Int64
)

func getPacketBuf() []byte {
	outstanding.Add(1)
	return *packetBufPool.Get().(*[]byte)
}

func putPacketBuf(buf []byte) {
	if cap(buf) != MaxPacketSize {
		// 扩容过或根本不是池里的对象：交给 GC，别把池的水位顶高
		return
	}
	outstanding.Add(-1)
	whole := buf[:cap(buf)] // 归还前还原全长，否则下一个 Get 拿到被削短过的切片
	packetBufPool.Put(&whole)
}

func borrowedPacketBufs() int64 { return outstanding.Load() }

type PacketConn struct {
	net.PacketConn
	input  chan *packetInfo
	output chan *packetInfo
	src    net.Addr
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *PacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	// relay 只使用 ReadWithMetadata;此方法仅为满足接口,不应被调用
	return 0, nil, common.NewError("tproxy packet conn does not implement ReadFrom, use ReadWithMetadata")
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, common.NewError("tproxy packet conn does not implement WriteTo, use WriteWithMetadata")
}

func (c *PacketConn) Close() error {
	c.cancel()
	drain := func(ch <-chan *packetInfo) {
		for {
			select {
			case info := <-ch:
				// 队列里每条都攥着一个从池里借出的负载；会话被拆时没有消费端再来取，
				// 不归还它们就永久离开池（堆内存能被回收，但池被抽空，之后每包重新 make 8KB）
				putPacketBuf(info.payload)
			default:
				return
			}
		}
	}
	drain(c.input)
	drain(c.output)
	return nil
}

func (c *PacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	// 必须 copy：proxy 的转发环在本方法返回后立刻把 p 归还给 buffer 池复用，
	// 直接把 p 入队会让队列里的包被下一个会话覆写。make 而不 copy 更会让 payload
	// 恒为全零（此前就是这个状态），与 socks/conn.go 的正确写法对齐。
	// 现在这块副本也从 packetBufPool 借：超尺寸的负载不截断，走一次性分配，
	// 归还时 putPacketBuf 的 cap 闸门会自动把它丢掉（不进池、不计入借出）。
	var newP []byte
	if len(p) <= MaxPacketSize {
		newP = getPacketBuf()
	} else {
		newP = make([]byte, len(p))
	}
	n := copy(newP, p)
	select {
	case c.output <- &packetInfo{
		metadata: m,
		payload:  newP[:n],
	}:
		return n, nil
	case <-c.ctx.Done():
		putPacketBuf(newP)
		return 0, common.NewError("socks packet conn closed")
	}
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	select {
	case info := <-c.input:
		n := copy(p, info.payload)
		// 负载已复制进调用方的 buffer，这份借出的副本到此为止；metadata 是 dispatch
		// 现场新建的，不引用 payload，所以归还不会连带把地址一起回收。
		putPacketBuf(info.payload)
		return n, info.metadata, nil
	case <-c.ctx.Done():
		return 0, nil, common.NewError("socks packet conn closed")
	}
}
