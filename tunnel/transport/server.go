package transport

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// firstByteTimeout 限定 HTTP 嗅探阶段等待对端数据的时限
const firstByteTimeout = 30 * time.Second

// Server is a server of transport layer
type Server struct {
	tcpListener net.Listener
	cmd         *exec.Cmd
	connChan    chan tunnel.Conn
	wsChan      chan tunnel.Conn
	httpLock    sync.RWMutex
	nextHTTP    bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	// closeOnce/closeErr：服务端栈里多个端点会共用同一个 transport 实例
	// （tls 层之下），Close 因此可能被调用多次。整个关闭流程只跑一次，
	// 后续调用复用首次结果。
	closeOnce sync.Once
	closeErr  error
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
	})
	return s.closeErr
}

func (s *Server) close() error {
	log.Info("[Transport Server] Closing transport server")
	s.cancel()
	err := s.tcpListener.Close()
	s.wg.Wait()
	// wg.Wait() 之后 acceptLoop 和 handler 都已退出、不再向 channel 发送,
	// 排空已完成但未被 AcceptConn 取走的连接,否则 connChan/wsChan(各 cap 32)
	// 里的连接会带着 fd 一直滞留到进程结束(与 tls 层同型问题)
drain:
	for {
		select {
		case c := <-s.connChan:
			c.Close() //gosec:disable -- 关闭滞留连接,忽略 close 错误
		case c := <-s.wsChan:
			c.Close() //gosec:disable -- 关闭滞留连接,忽略 close 错误
		default:
			break drain
		}
	}
	if s.cmd != nil && s.cmd.Process != nil {
		log.Debug("[Transport Server] Killing transport plugin process")
		s.cmd.Process.Kill() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		s.cmd.Wait()         //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		log.Info("[Transport Server] Transport plugin process killed")
	}
	log.Info("[Transport Server] Transport server closed successfully")
	return err
}

func (s *Server) acceptLoop() {
	for {
		tcpConn, err := s.tcpListener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			log.Error(common.NewError("transport accept error").Base(err))
			select {
			case <-time.After(time.Millisecond * 100):
			case <-s.ctx.Done():
				return
			}
			continue
		}

		s.wg.Go(func() {
			s.handleConnection(tcpConn)
		})
	}
}

func (s *Server) handleConnection(tcpConn net.Conn) {
	log.Debug("tcp connection from", tcpConn.RemoteAddr())
	s.httpLock.RLock()
	isHTTP := s.nextHTTP
	s.httpLock.RUnlock()

	if isHTTP { // plaintext mode enabled
		rewindConn := common.NewRewindConn(tcpConn)
		rewindConn.SetBufferSize(512)

		// 对端静默时不设截止时间会让 handler 永久阻塞，Close() 的 wg.Wait() 随之挂起
		tcpConn.SetDeadline(time.Now().Add(firstByteTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		// 读取侧独立限幅：http.ReadRequest 自身没有 header 上限，未认证对端可以
		// 只用请求头就把单连接内存推到数百 MB。这里读走的字节仍会被 rewindConn
		// 记录并可回放，所以截断只影响嗅探结果，不会丢正常握手的数据。
		r := bufio.NewReader(io.LimitReader(rewindConn, common.MaxSniffRequestBytes))
		httpReq, err := http.ReadRequest(r)
		rewindConn.Rewind()
		rewindConn.StopBuffering()
		// 连接即将移交下游长期使用，必须解除截止时间
		tcpConn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		if err != nil {
			log.Debug("failed to parse http request, treating as trojan connection:", err)
			select {
			case s.connChan <- &Conn{
				Conn: rewindConn,
			}:
			case <-s.ctx.Done():
				rewindConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		} else {
			log.Debug("plaintext http request: ", httpReq)
			select {
			case s.wsChan <- &Conn{
				Conn: rewindConn,
			}:
			case <-s.ctx.Done():
				rewindConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		}
	} else {
		select {
		case s.connChan <- &Conn{
			Conn: tcpConn,
		}:
		case <-s.ctx.Done():
			tcpConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
	}
}

func (s *Server) AcceptConn(overlay tunnel.Tunnel) (tunnel.Conn, error) {
	// 使用 overlay.Name() 字符串比较而非类型断言：
	// tunnel/transport 导入 tunnel/websocket/tunnel/http 会形成 import cycle，
	// 字符串比较是合理的解耦方式，无需引入新接口。
	if overlay != nil && (overlay.Name() == "WEBSOCKET" || overlay.Name() == "HTTP") {
		s.httpLock.Lock()
		s.nextHTTP = true
		s.httpLock.Unlock()
		select {
		case conn := <-s.wsChan:
			return conn, nil
		case <-s.ctx.Done():
			return nil, common.NewError("transport server closed")
		}
	}
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("transport server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("transport does not support packet accept")
}

// NewServer creates a transport layer server
func NewServer(ctx context.Context, _ tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	listenAddress := tunnel.NewAddressFromHostPort("tcp", cfg.LocalHost, cfg.LocalPort)

	var cmd *exec.Cmd
	if cfg.TransportPlugin.Enabled {
		log.Warn("transport server will use plugin and work in plain text mode")
		switch cfg.TransportPlugin.Type {
		case "shadowsocks":
			trojanHost := "127.0.0.1"
			trojanPort := common.PickPort("tcp", trojanHost)
			cfg.TransportPlugin.Env = append(
				cfg.TransportPlugin.Env,
				"SS_REMOTE_HOST="+cfg.LocalHost,
				"SS_REMOTE_PORT="+strconv.FormatInt(int64(cfg.LocalPort), 10),
				"SS_LOCAL_HOST="+trojanHost,
				"SS_LOCAL_PORT="+strconv.FormatInt(int64(trojanPort), 10),
				"SS_PLUGIN_OPTIONS="+cfg.TransportPlugin.Option,
			)

			cfg.LocalHost = trojanHost
			cfg.LocalPort = trojanPort
			listenAddress = tunnel.NewAddressFromHostPort("tcp", cfg.LocalHost, cfg.LocalPort)
			log.Debug("new listen address", listenAddress)
			log.Debug("plugin env", cfg.TransportPlugin.Env)

			if err := validatePluginCommand(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg); err != nil {
				return nil, common.NewError("invalid transport plugin configuration").Base(err)
			}
			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...) //gosec:disable -- 已通过 validatePluginCommand 校验
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := cmd.Start(); err != nil {
				return nil, common.NewError("failed to start transport plugin").Base(err)
			}
		case "other":
			if err := validatePluginCommand(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg); err != nil {
				return nil, common.NewError("invalid transport plugin configuration").Base(err)
			}
			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...) //gosec:disable -- 已通过 validatePluginCommand 校验
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := cmd.Start(); err != nil {
				return nil, common.NewError("failed to start transport plugin").Base(err)
			}
		case "plaintext":
			// do nothing
		default:
			return nil, common.NewError("invalid plugin type: " + cfg.TransportPlugin.Type)
		}
	}
	listenCfg := common.ListenConfig{
		EnableTFO: cfg.TCP.FastOpen,
	}
	tcpListener, err := common.Listen(ctx, listenCfg, "tcp", listenAddress.String())
	if err != nil {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			cmd.Wait()         //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
		return nil, common.NewError("transport failed to listen").Base(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		tcpListener: tcpListener,
		cmd:         cmd,
		ctx:         ctx,
		cancel:      cancel,
		connChan:    make(chan tunnel.Conn, 32),
		wsChan:      make(chan tunnel.Conn, 32),
	}
	server.wg.Go(func() {
		server.acceptLoop()
	})
	return server, nil
}
