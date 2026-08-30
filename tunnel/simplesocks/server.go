package simplesocks

import (
	"context"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/recorder"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// Server is a simplesocks server
type Server struct {
	underlay   tunnel.Server
	connChan   chan tunnel.Conn
	packetChan chan tunnel.PacketConn
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func (s *Server) Close() error {
	s.cancel()
	err := s.underlay.Close()
	s.wg.Wait()
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			log.Error(common.NewError("simplesocks failed to accept connection from underlying tunnel").Base(err))
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				return
			}
			continue
		}
		metadata := new(tunnel.Metadata)
		_, err = metadata.ReadFrom(conn)
		if err != nil {
			log.Error(common.NewError("simplesocks server faield to read header").Base(err))
			conn.Close()
			continue
		}
		switch metadata.Command {
		case Connect:
			select {
			case s.connChan <- &Conn{
				metadata: metadata,
				Conn:     conn,
			}:
			case <-s.ctx.Done():
				// 下游已停止消费，关闭连接并退出，否则 Close() 的 wg.Wait() 死锁
				conn.Close()
				return
			}
			Record(conn, metadata)
		case Associate:
			select {
			case s.packetChan <- &PacketConn{
				Conn: conn,
			}:
			case <-s.ctx.Done():
				conn.Close()
				return
			}
		default:
			log.Error(common.NewErrorf("simplesocks unknown command %d", metadata.Command))
			conn.Close()
		}
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("simplesocks server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case packetConn := <-s.packetChan:
		return packetConn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("simplesocks server closed")
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		underlay:   underlay,
		ctx:        ctx,
		connChan:   make(chan tunnel.Conn, 32),
		packetChan: make(chan tunnel.PacketConn, 32),
		cancel:     cancel,
	}
	server.wg.Go(func() {
		server.acceptLoop()
	})
	log.Debug("simplesocks server created")
	return server, nil
}

func Record(conn tunnel.Conn, metadata *tunnel.Metadata) {
	var userHash string
	if muxConn, ok := conn.(*mux.Conn); ok {
		c := muxConn.Conn
		if trojanConn, ok2 := c.(*trojan.InboundConn); ok2 {
			userHash = trojanConn.Hash()
		}
	}
	if userHash != "" {
		log.Debug("user", userHash, "from", conn.RemoteAddr(), "tunneling to", metadata.Address)
		recorder.Add(userHash, conn.RemoteAddr(), metadata.Address, "TCP", nil)
	}
}
