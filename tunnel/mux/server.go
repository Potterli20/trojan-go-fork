package mux

import (
	"context"
	"fmt"
	"sync"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// Server is a smux server
type Server struct {
	underlay tunnel.Server
	connChan chan tunnel.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	// sessions 是在途的 smux 会话。handleSession 阻塞在 AcceptStream 上，
	// 而那个调用不看 s.ctx，也不受底层监听器关闭影响（连接是上层交下来的，
	// underlay.Close 只关监听器），所以必须留一份登记表供 Close 显式关闭。
	sessionLock sync.Mutex
	sessions    map[*smux.Session]struct{}
}

func (s *Server) addSession(session *smux.Session) bool {
	s.sessionLock.Lock()
	defer s.sessionLock.Unlock()
	if s.ctx.Err() != nil {
		// 已经在关停了：不能再登记，否则 Close 的排空已经跑完，
		// 这个会话就没人能唤醒它
		return false
	}
	s.sessions[session] = struct{}{}
	return true
}

func (s *Server) dropSession(session *smux.Session) {
	s.sessionLock.Lock()
	delete(s.sessions, session)
	s.sessionLock.Unlock()
}

// closeSessions 关掉所有在途会话，把阻塞在 AcceptStream 上的 handleSession 唤醒。
func (s *Server) closeSessions() {
	s.sessionLock.Lock()
	pending := make([]*smux.Session, 0, len(s.sessions))
	for session := range s.sessions {
		pending = append(pending, session)
	}
	s.sessionLock.Unlock()
	for _, session := range pending {
		// 错误忽略：这些会话本来就是要被拆掉的
		_ = session.Close()
	}
}

func (s *Server) acceptConnWorker() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				return
			}
			log.Debug(err)
			continue
		}

		tracker := log.NewConnectionTracker("MUX", "AcceptConn").
			WithField("remote_addr", conn.RemoteAddr().String())

		s.wg.Go(func() {
			s.handleConn(conn, tracker)
		})
	}
}

func (s *Server) handleConn(conn tunnel.Conn, tracker *log.ConnectionTracker) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic in mux handler: ", fmt.Sprintf("%v", r))
			tracker.Error(common.NewErrorf("panic: %v", r)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			conn.Close()                                    //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
	}()

	smuxConfig := smux.DefaultConfig()
	smuxSession, err := smux.Server(conn, smuxConfig)
	if err != nil {
		log.Error(err)
		_ = tracker.Error(err)
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return
	}
	_ = tracker.Success()
	if !s.addSession(smuxSession) {
		_ = smuxSession.Close()
		conn.Close() //gosec:disable -- 错误忽略：关停路径，尽力关闭
		return
	}
	s.wg.Go(func() {
		defer s.dropSession(smuxSession)
		s.handleSession(smuxSession, conn)
	})
}

func (s *Server) handleSession(session *smux.Session, conn tunnel.Conn) {
	defer session.Close()
	defer conn.Close()
	// 这条会话上的每个 stream 共用同一个底层 conn，remote_addr 是不变量；但
	// RemoteAddr().String() 每次都要重新格式化（实测 125 ns、3 allocs），而这个循环是
	// 每接一个 stream 走一遍——浏览器一个 mux 会话轻松几十上百条流。提到循环外算一次。
	remoteAddr := conn.RemoteAddr().String()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			log.Error(err)
			return
		}

		streamTracker := log.NewConnectionTracker("MUX", "Stream").
			WithField("remote_addr", remoteAddr)

		select {
		case s.connChan <- &Conn{
			rwc:     stream,
			Conn:    conn,
			tracker: streamTracker,
		}:
			_ = streamTracker.Success()
		case <-s.ctx.Done():
			log.Debug("exiting")
			return
		}
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("mux server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("mux does not support packet accept")
}

func (s *Server) Close() error {
	s.cancel()
	// AcceptStream 不看 ctx，且会话的底层连接不属于本层监听器：只 cancel +
	// underlay.Close 的话，每个已建立的会话都会让下面的 wg.Wait 永久挂住，
	// 连带泄漏 1 个 goroutine + 会话 + fd（外层只靠 5s 的 waitBounded 掩盖，
	// 资源照样没回收）
	s.closeSessions()
	err := s.underlay.Close()
	s.wg.Wait()
	return err
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		underlay: underlay,
		ctx:      ctx,
		cancel:   cancel,
		connChan: make(chan tunnel.Conn, 32),
		sessions: make(map[*smux.Session]struct{}),
	}
	server.wg.Go(func() {
		server.acceptConnWorker()
	})
	log.Debug("mux server created")
	return server, nil
}
