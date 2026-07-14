package websocket

import (
	"bufio"
	"context"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

type handshakeState int

const (
	handshakeStateIdle handshakeState = iota
	handshakeStateInProgress
	handshakeStateCompleted
	handshakeStateFailed
	handshakeStateTimeout
)

type handshakeManager struct {
	state     handshakeState
	stateChan chan handshakeState
	done      chan struct{}
	mutex     sync.RWMutex
}

func newHandshakeManager() *handshakeManager {
	return &handshakeManager{
		state:     handshakeStateIdle,
		stateChan: make(chan handshakeState, 1),
		done:      make(chan struct{}),
	}
}

func (hm *handshakeManager) setState(state handshakeState) {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	hm.state = state
	select {
	case hm.stateChan <- state:
	default:
	}
}

func (hm *handshakeManager) getState() handshakeState {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	return hm.state
}

func (hm *handshakeManager) waitCompletedOrTimeout(timeout time.Duration) handshakeState {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case state := <-hm.stateChan:
			if state == handshakeStateCompleted || state == handshakeStateFailed {
				return state
			}
		case <-timer.C:
			hm.setState(handshakeStateTimeout)
			return handshakeStateTimeout
		case <-hm.done:
			return hm.getState()
		}
	}
}

func (hm *handshakeManager) close() {
	close(hm.done)
}

// Fake response writer
// Websocket ServeHTTP method uses Hijack method to get the ReadWriter
type fakeHTTPResponseWriter struct {
	http.Hijacker
	http.ResponseWriter

	ReadWriter *bufio.ReadWriter
	Conn       net.Conn
}

func (w *fakeHTTPResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.Conn, w.ReadWriter, nil
}

type Server struct {
	underlay  tunnel.Server
	hostname  string
	path      string
	enabled   bool
	redirAddr net.Addr
	redir     *redirector.Redirector
	ctx       context.Context
	cancel    context.CancelFunc
	timeout   time.Duration
	wg        sync.WaitGroup
}

func (s *Server) Close() error {
	s.cancel()
	s.wg.Wait()
	return s.underlay.Close()
}

func (s *Server) cleanupFailedHandshake(conn tunnel.Conn, tracker *log.ConnectionTracker, err error) error {
	if transportConn, ok := conn.(*transport.Conn); ok {
		if rewindConn, ok := transportConn.Conn.(*common.RewindConn); ok {
			rewindConn.Rewind()
			rewindConn.StopBuffering()
		}
	}
	_ = tracker.Error(err)
	s.redir.Redirect(&redirector.Redirection{
		InboundConn: conn,
		RedirectTo:  s.redirAddr,
	})
	return err
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.underlay.AcceptConn(&Tunnel{})
	if err != nil {
		return nil, common.NewError("websocket failed to accept connection from underlying server")
	}

	tracker := log.NewConnectionTracker("WebSocket", "AcceptConn").
		WithField("remote_addr", conn.RemoteAddr().String()).
		WithField("path", s.path)

	log.Debugf("[WebSocket] [conn=%s] New connection accepted from %s, path=%s, enabled=%v",
		tracker.ConnID(), conn.RemoteAddr().String(), s.path, s.enabled)

	if !s.enabled {
		err := common.NewError("websocket is disabled. redirecting http request from " + conn.RemoteAddr().String())
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	req, err := http.ReadRequest(rw.Reader)
	if err != nil {
		log.Debug("invalid http request")
		err = common.NewError("not a valid http request: " + conn.RemoteAddr().String()).Base(err)
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}
	if strings.ToLower(req.Header.Get("Upgrade")) != "websocket" || req.URL.Path != s.path {
		log.Debug("invalid http websocket handshake request")
		err = common.NewError("not a valid websocket handshake request: " + conn.RemoteAddr().String()).Base(err)
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}

	handshakeMgr := newHandshakeManager()
	handshakeMgr.setState(handshakeStateInProgress)

	url := "wss://" + s.hostname + s.path
	origin := "https://" + s.hostname
	wsConfig, err := websocket.NewConfig(url, origin)
	if err != nil {
		return nil, common.NewError("failed to create websocket config").Base(err)
	}
	var wsConn *websocket.Conn
	ctx, cancel := context.WithCancel(s.ctx)

	wsServer := websocket.Server{
		Config: *wsConfig,
		Handler: func(conn *websocket.Conn) {
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("[WebSocket] [conn=%s] Handler panic: %v", tracker.ConnID(), r)
					handshakeMgr.setState(handshakeStateFailed)
				}
			}()

			wsConn = conn
			wsConn.PayloadType = websocket.BinaryFrame

			log.Debugf("[WebSocket] [conn=%s] Handshake completed, protocol=%s",
				tracker.ConnID(), req.Header.Get("Upgrade"))

			handshakeMgr.setState(handshakeStateCompleted)

			<-ctx.Done()
			log.Debugf("[WebSocket] [conn=%s] Connection closed", tracker.ConnID())
		},
		Handshake: func(wsConfig *websocket.Config, httpRequest *http.Request) error {
			log.Debugf("[WebSocket] [conn=%s] Handshake request: url=%s, origin=%s",
				tracker.ConnID(), httpRequest.URL.String(), httpRequest.Header.Get("Origin"))
			return nil
		},
	}

	respWriter := &fakeHTTPResponseWriter{
		Conn:       conn,
		ReadWriter: rw,
	}

	s.wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("[WebSocket] [conn=%s] ServeHTTP panic: %v", tracker.ConnID(), r)
				if handshakeMgr.getState() == handshakeStateInProgress {
					handshakeMgr.setState(handshakeStateFailed)
				}
			}
		}()
		wsServer.ServeHTTP(respWriter, req)
	})

	finalState := handshakeMgr.waitCompletedOrTimeout(s.timeout)

	if finalState != handshakeStateCompleted {
		log.Warnf("[WebSocket] [conn=%s] Handshake failed with state: %d", tracker.ConnID(), finalState)

		cancel()
		conn.Close()
		handshakeMgr.close()

		var err error
		if finalState == handshakeStateTimeout {
			err = common.NewError("websocket handshake timeout")
		} else {
			err = common.NewError("websocket failed to handshake")
		}
		_ = tracker.Error(err)
		return nil, err
	}

	_ = tracker.Success()
	return &InboundConn{
		OutboundConn: OutboundConn{
			tcpConn: conn,
			Conn:    wsConn,
		},
		ctx:     ctx,
		cancel:  cancel,
		tracker: tracker,
	}, nil
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported")
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.Websocket.Enabled {
		if !strings.HasPrefix(cfg.Websocket.Path, "/") {
			return nil, common.NewError("websocket path must start with \"/\"")
		}
	}
	if cfg.RemoteHost == "" {
		log.Warn("empty websocket redirection hostname")
		cfg.RemoteHost = cfg.Websocket.Host
	}
	if cfg.RemotePort == 0 {
		log.Warn("empty websocket redirection port")
		cfg.RemotePort = 80
	}
	ctx, cancel := context.WithCancel(ctx)
	log.Debug("websocket server created")

	return &Server{
		enabled:   cfg.Websocket.Enabled,
		hostname:  cfg.Websocket.Host,
		path:      cfg.Websocket.Path,
		ctx:       ctx,
		cancel:    cancel,
		underlay:  underlay,
		timeout:   time.Second * time.Duration(rand.IntN(10)+5),
		redir:     redirector.NewRedirector(ctx),
		redirAddr: tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort),
	}, nil
}
