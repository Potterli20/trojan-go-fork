package simplesocks

import (
	"context"
	"sync"
	"time"

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

// headerTimeout 限定读取 simplesocks 头的时限。acceptLoop 是**单个串行** goroutine：
// 一条被打开却始终不下发字节的流会把它整个卡住，此后所有 mux 会话都建不起来，
// 而 Close() 的 wg.Wait() 也只能靠 proxy 的 5s 有界兜底才返回。
// 与 trojan 的 authTimeout 同型；声明成 var 是为了让测试能把等待压到毫秒级。
var headerTimeout = 10 * time.Second

// readHeader 把读头放到独立 goroutine 里，超时或关停时关闭该流。
// 这里不能用 SetDeadline：smux 的 Stream 没有 deadline 方法，而 mux.Conn 上解析到的
// SetDeadline 落在**内嵌的会话 TCP 连接**上——在那儿设时限会波及同一会话的其它流。
// 关闭流则一定奏效：被抛弃的那次 Read 会立即返回错误，所以不留滞留 goroutine。
func (s *Server) readHeader(conn tunnel.Conn) (*tunnel.Metadata, error) {
	type outcome struct {
		metadata *tunnel.Metadata
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		metadata := new(tunnel.Metadata)
		_, err := metadata.ReadFrom(conn)
		result <- outcome{metadata: metadata, err: err}
	}()

	timer := time.NewTimer(headerTimeout)
	defer timer.Stop()

	select {
	case o := <-result:
		return o.metadata, o.err
	case <-timer.C:
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("timed out waiting for simplesocks header")
	case <-s.ctx.Done():
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("simplesocks server closed")
	}
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
		metadata, err := s.readHeader(conn)
		if err != nil {
			log.Error(common.NewError("simplesocks server failed to read header").Base(err))
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				return
			}
			Record(conn, metadata)
		case Associate:
			select {
			case s.packetChan <- &PacketConn{
				Conn: conn,
			}:
			case <-s.ctx.Done():
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				return
			}
		default:
			log.Error(common.NewErrorf("simplesocks unknown command %d", metadata.Command))
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
