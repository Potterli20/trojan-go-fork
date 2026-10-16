package socks

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// packetBufPool 复用 UDP 负载缓冲，口径与 tproxy/dokodemo 一致：此前这条链上每包各
// make 一次（dispatch 的 `make([]byte, remaining)`、WriteWithMetadata 的 `make(len p)`），
// 分配次数与包数同阶，而队列深度 16、负载最大 8KB 的驻留本来就是按池的形状设计的。
// 借用权随 packetInfo 交给消费端（守护协程 / ReadWithMetadata），用完归还；会话被拆时
// `Close` 排空两条队列并逐个归还（同 tunnel/router 那轮补过的不变量）。
//
// outstanding 是给判据用的计数：Get 加、Put 减。pool_account_test.go 要求一轮突发 +
// 拆会话之后归零；为负说明有 buffer 被归还了两次——那比漏归还严重，两个包会共享同一块
// 底层数组、互相串数据。
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
		// 超尺寸或根本不是池里的对象：交给 GC，别把池的水位顶高
		return
	}
	outstanding.Add(-1)
	whole := buf[:cap(buf)] // 归还前还原全长，否则下一个 Get 拿到被削短过的切片
	packetBufPool.Put(&whole)
}

func borrowedPacketBufs() int64 { return outstanding.Load() }

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
	return 0, nil, common.NewError("socks packet conn does not implement ReadFrom, use ReadWithMetadata")
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, common.NewError("socks packet conn does not implement WriteTo, use WriteWithMetadata")
}

func (c *PacketConn) Close() error {
	c.cancel()
	drain := func(ch <-chan *packetInfo) {
		for {
			select {
			case info := <-ch:
				// 队列里每条都攥着一块借出的负载；会话被拆时没有消费端再来取，
				// 不归还它们就永久离开池
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
	// 直接把 p 入队会让队列里的包被下一个会话覆写（判据 no_retain_test.go）。
	// 超尺寸的负载不截断，走一次性分配；putPacketBuf 的 cap 闸门会自动把它丢掉。
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
		// 负载已复制进调用方的 buffer；metadata 是 dispatch 现场新建的，不引用 payload
		putPacketBuf(info.payload)
		return n, info.metadata, nil
	case <-c.ctx.Done():
		return 0, nil, common.NewError("socks packet conn closed")
	}
}
