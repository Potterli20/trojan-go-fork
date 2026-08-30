package shadowsocks

import (
	"context"
	"net"
	"time"

	"github.com/Potterli20/go-shadowsocks2/core"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Server struct {
	core.Cipher
	*redirector.Redirector
	underlay  tunnel.Server
	redirAddr net.Addr
}

// probeTimeout 限定 AEAD 探测读(等待首个有效加密载荷)的时限
const probeTimeout = 10 * time.Second

func (s *Server) AcceptConn(overlay tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.underlay.AcceptConn(&Tunnel{})
	if err != nil {
		return nil, common.NewError("shadowsocks failed to accept connection from underlying tunnel").Base(err)
	}
	rewindConn := common.NewRewindConn(conn)
	rewindConn.SetBufferSize(1024)
	defer rewindConn.StopBuffering()

	// 探测读限时:对端静默(如端口扫描)时不让 handler 永久阻塞,
	// 进而卡死串行调用方的 accept 循环与 Close() 的 wg.Wait()
	conn.SetReadDeadline(time.Now().Add(probeTimeout))
	// 探测完成后必须解除截止时间:成功路径的连接移交下游长期使用,
	// 重定向路径的连接交给 redirector 持续拷贝,都不能再撞过期
	defer conn.SetReadDeadline(time.Time{})

	// try to read something from this connection
	buf := [1024]byte{}
	testConn := s.Cipher.StreamConn(rewindConn)
	if _, err := testConn.Read(buf[:]); err != nil {
		// we are under attack
		log.Error(common.NewError("shadowsocks failed to decrypt").Base(err))
		rewindConn.Rewind()
		rewindConn.StopBuffering()
		s.Redirect(&redirector.Redirection{
			RedirectTo:  s.redirAddr,
			InboundConn: rewindConn,
		})
		return nil, common.NewError("invalid aead payload")
	}
	rewindConn.Rewind()
	rewindConn.StopBuffering()

	return &Conn{
		aeadConn: s.Cipher.StreamConn(rewindConn),
		Conn:     conn,
	}, nil
}

func (s *Server) AcceptPacket(t tunnel.Tunnel) (tunnel.PacketConn, error) {
	panic("not supported")
}

func (s *Server) Close() error {
	return s.underlay.Close()
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	cipher, err := core.PickCipher(cfg.Shadowsocks.Method, nil, cfg.Shadowsocks.Password)
	if err != nil {
		return nil, common.NewError("invalid shadowsocks cipher").Base(err)
	}
	if cfg.RemoteHost == "" {
		return nil, common.NewError("invalid shadowsocks redirection address")
	}
	if cfg.RemotePort == 0 {
		return nil, common.NewError("invalid shadowsocks redirection port")
	}
	log.Debug("shadowsocks client created")
	return &Server{
		underlay:   underlay,
		Cipher:     cipher,
		Redirector: redirector.NewRedirector(ctx),
		redirAddr:  tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort),
	}, nil
}
