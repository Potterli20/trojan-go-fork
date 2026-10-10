//go:build linux

package tproxy

import (
	"context"
	"net"

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
			case <-ch:
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
	newP := make([]byte, len(p))
	copy(newP, p)
	select {
	case c.output <- &packetInfo{
		metadata: m,
		payload:  newP,
	}:
		return len(p), nil
	case <-c.ctx.Done():
		return 0, common.NewError("socks packet conn closed")
	}
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	select {
	case info := <-c.input:
		n := copy(p, info.payload)
		return n, info.metadata, nil
	case <-c.ctx.Done():
		return 0, nil, common.NewError("socks packet conn closed")
	}
}
