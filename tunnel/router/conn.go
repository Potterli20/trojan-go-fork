package router

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type packetInfo struct {
	src     *tunnel.Metadata
	payload []byte
}

type PacketConn struct {
	proxy tunnel.PacketConn
	net.PacketConn
	packetChan chan *packetInfo
	*Client
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	tracker *log.ConnectionTracker
}

// maxConsecutivePacketErrors 连续读失败多少次后判定会话永久故障。
// 底层会话可能已被 trojan 层关闭并返回非 EOF 的包装错误
// (common 错误无 Unwrap，errors.Is 无法穿透识别)，
// 此时持续重试只会以 10Hz 空转且消费端永久阻塞
const maxConsecutivePacketErrors = 10

func (c *PacketConn) packetLoop() {
	c.wg.Go(func() {
		consecutiveErrors := 0
		for {
			if c.ctx.Err() != nil {
				return
			}
			buf := make([]byte, MaxPacketSize)
			n, addr, err := c.proxy.ReadWithMetadata(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
				// 直接检查 ctx.Err()
				if c.ctx.Err() != nil {
					return
				}
				consecutiveErrors++
				log.Error("router packetConn error", err)
				if consecutiveErrors >= maxConsecutivePacketErrors {
					log.Error("router proxy packetConn persistently failing, stopping packet loop")
					// 取消自身 ctx：消费端的 ReadWithMetadata/ReadFrom 随 ctx.Done 返回 EOF 干净退出
					c.cancel()
					return
				}
				time.Sleep(time.Millisecond * 100) // 避免 busy loop
				continue
			}
			consecutiveErrors = 0
			select {
			case c.packetChan <- &packetInfo{
				src:     addr,
				payload: buf[:n],
			}:
			case <-c.ctx.Done():
				return
			}
		}
	})
	c.wg.Go(func() {
		consecutiveErrors := 0
		for {
			if c.ctx.Err() != nil {
				return
			}
			buf := make([]byte, MaxPacketSize)
			n, addr, err := c.PacketConn.ReadFrom(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
				// 直接检查 ctx.Err()
				if c.ctx.Err() != nil {
					return
				}
				consecutiveErrors++
				log.Error("router packetConn error", err)
				if consecutiveErrors >= maxConsecutivePacketErrors {
					log.Error("router direct packetConn persistently failing, stopping packet loop")
					c.cancel()
					return
				}
				time.Sleep(time.Millisecond * 100) // 避免 busy loop
				continue
			}
			consecutiveErrors = 0
			address, _ := tunnel.NewAddressFromAddr("udp", addr.String())
			select {
			case c.packetChan <- &packetInfo{
				src: &tunnel.Metadata{
					Address: address,
				},
				payload: buf[:n],
			}:
			case <-c.ctx.Done():
				return
			}
		}
	})
}

func (c *PacketConn) Close() error {
	c.cancel()
	c.proxy.Close()
	if c.tracker != nil {
		c.tracker.Destroy("closed", 0, 0)
	}
	err := c.PacketConn.Close()
	c.wg.Wait()
	return err
}

func (c *PacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	select {
	case info := <-c.packetChan:
		n := copy(p, info.payload)
		return n, info.src.Address, nil
	case <-c.ctx.Done():
		return 0, nil, io.EOF
	}
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, common.NewError("invalid UDP address")
	}
	address, err := tunnel.NewAddressFromAddr("udp", udpAddr.String())
	if err != nil {
		return 0, common.NewError("failed to create tunnel address").Base(err)
	}
	metadata := &tunnel.Metadata{
		Address: address,
	}
	return c.WriteWithMetadata(p, metadata)
}

func (c *PacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	policy := c.Route(m.Address)
	switch policy {
	case Proxy:
		return c.proxy.WriteWithMetadata(p, m)
	case Block:
		return 0, common.NewError("router blocked address (udp): " + m.Address.String())
	case Bypass:
		ip, err := m.Address.ResolveIP()
		if err != nil {
			return 0, common.NewError("router failed to resolve udp address").Base(err)
		}
		return c.PacketConn.WriteTo(p, &net.UDPAddr{
			IP:   ip,
			Port: m.Address.Port,
		})
	default:
		panic("unknown policy")
	}
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	select {
	case info := <-c.packetChan:
		n := copy(p, info.payload)
		return n, info.src, nil
	case <-c.ctx.Done():
		return 0, nil, io.EOF
	}
}
