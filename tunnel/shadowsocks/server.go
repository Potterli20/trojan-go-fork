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

func (s *Server) AcceptConn(overlay tunnel.Tunnel) (result tunnel.Conn, resultErr error) {
	conn, err := s.underlay.AcceptConn(&Tunnel{})
	if err != nil {
		return nil, common.NewError("shadowsocks failed to accept connection from underlying tunnel").Base(err)
	}
	// 密文探测（下面的 StreamConn + Read）解析的是**未认证对端**的字节。父层的 accept
	// 循环现在带着 panic 边界（tunnel.AcceptConnGuarded），循环不会再被带走，但栈展开
	// 之后这条 fd 既没交出也没关闭的话就是每命中一次漏一条——所以在这里补最后一道
	// 回收，口径同 tunnel/websocket 的 handedOff。
	// takenOver 表示所有权已经交出去：成功返回给下游，或已交给 redirector 做误导中继
	// （那条路径上 result 也是 nil，绝不能顺手关掉正在被拷贝的连接）。
	takenOver := false
	defer func() {
		if !takenOver && result == nil {
			conn.Close() //gosec:disable -- 错误忽略：panic 或未预期失败路径的兜底回收
		}
	}()
	rewindConn := common.NewRewindConn(conn)
	rewindConn.SetBufferSize(1024)
	defer rewindConn.StopBuffering()

	// 探测读限时:对端静默(如端口扫描)时不让 handler 永久阻塞,
	// 进而卡死串行调用方的 accept 循环与 Close() 的 wg.Wait()
	conn.SetReadDeadline(time.Now().Add(probeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
		takenOver = true
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
	return nil, common.NewError("shadowsocks does not support packet accept")
}

func (s *Server) Close() error {
	err := s.underlay.Close()
	// 这里嵌的是 *redirector.Redirector，而 Server 自己又定义了 Close，
	// 嵌进来的那个 Close 被遮蔽了：不显式调用，重定向器就永远没人取消它的 ctx，
	// 队列里（容量 64）和在中继中的会话各自攥着一根未认证对端的活连接。
	s.Redirector.Close() //gosec:disable -- Redirector.Close 恒返回 nil；关停路径尽力回收
	return err
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
