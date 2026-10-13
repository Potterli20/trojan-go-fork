package socks

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const (
	Connect   tunnel.Command = 1
	Associate tunnel.Command = 3
)

const (
	MaxPacketSize = 1024 * 8
)

// handshakeTimeout 限定 socks 握手(version/method/command/address)整体时限
const handshakeTimeout = 30 * time.Second

type Server struct {
	connChan         chan tunnel.Conn
	packetChan       chan tunnel.PacketConn
	underlay         tunnel.Server
	localHost        string
	localPort        int
	timeout          time.Duration
	listenPacketConn tunnel.PacketConn
	mapping          map[string]*PacketConn
	mappingLock      sync.RWMutex
	// associations 是在途 UDP ASSOCIATE 的控制连接。它们既不下发给下游、
	// 也不属于 mapping（mapping 按 UDP 源地址索引，和 TCP 源地址无关），
	// 所以必须单独登记，否则 Server.Close 没有任何办法把它们关掉。
	assocLock    sync.Mutex
	associations map[tunnel.Conn]struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// associationMaxLife 是单条 ASSOCIATE 控制连接的最长生命。
// RFC 1928 把这条连接当作 UDP 关联的生命周期，所以不能「回完响应就关」；
// 但只靠对端 FIN 回收也不行：custom 栈可以把 socks 入站放到公网监听上，
// 一个「建好关联就发呆」的对端能无上限地占住 fd + goroutine。
// 到点关掉即可：UDP 中继会话本身有空闲超时，客户端重关联就能继续用。
const associationMaxLife = 10 * time.Minute

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) acceptConnLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			log.Error(common.NewError("socks failed to accept conn").Base(err))
			continue
		}
		s.wg.Go(func() {
			// 握手期间对端可能静默(如端口扫描);截止时间覆盖 version/method/
			// command/address 全部握手读,移交下游前解除
			conn.SetDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			handledConn, err := s.handshake(conn)
			if err != nil {
				log.Error(common.NewError("socks failed to handshake").Base(err))
				conn.Close() // handshake 失败时确保关闭原始连接 //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				return
			}
			// 握手成功的连接所有权移交给上层（connChan 的使用方负责关闭），
			// 此处不能 defer Close，否则连接在移交给 connChan 后会被立即关闭
			switch handledConn.Metadata().Command {
			case Connect:
				log.Info("socks connect request from", handledConn.RemoteAddr(), "metadata", handledConn.Metadata())
				err = s.connect(handledConn)
				if err != nil {
					log.Error(common.NewError("socks failed to respond connect").Base(err))
					handledConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
				// 连接即将移交下游长期使用,必须解除握手期截止时间
				conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				select {
				case s.connChan <- handledConn:
				case <-s.ctx.Done():
					log.Debug("exiting")
					handledConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				}
			case Associate:
				log.Info("socks associate request from", handledConn.RemoteAddr(), "metadata", handledConn.Metadata())
				err = s.associate(handledConn, handledConn.Metadata().Address)
				if err != nil {
					log.Error(common.NewError("socks failed to respond associate").Base(err))
					handledConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
				// 响应已发出，这条 TCP 连接按 RFC 是 UDP 关联的生命周期凭据，既不能
				// 交给下游也不能就此弃用：旧实现两件事都没做，每来一个 ASSOCIATE 就
				// 永久留下一个 fd（没人再读它，对端 FIN 也不会让本端 socket 消失）。
				s.watchAssociation(handledConn)
			default:
				log.Error("socks unknown command", handledConn.Metadata().Command)
				handledConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		})
	}
}

func (s *Server) Close() error {
	s.cancel()
	// 在途关联的读循环不看 ctx，不显式关掉它们的话下面的 wg.Wait 永远回不来
	s.closeAssociations()
	s.listenPacketConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	err := s.underlay.Close()
	s.wg.Wait()
	return err
}

func (s *Server) handshake(conn net.Conn) (*Conn, error) {
	version := [1]byte{}
	if _, err := io.ReadFull(conn, version[:]); err != nil {
		return nil, common.NewError("failed to read socks version").Base(err)
	}
	if version[0] != 5 {
		return nil, common.NewErrorf("invalid socks version %d", version[0])
	}
	nmethods := [1]byte{}
	if _, err := io.ReadFull(conn, nmethods[:]); err != nil {
		return nil, common.NewError("failed to read NMETHODS").Base(err)
	}
	if _, err := io.CopyN(io.Discard, conn, int64(nmethods[0])); err != nil {
		return nil, common.NewError("socks failed to read methods").Base(err)
	}
	if _, err := conn.Write([]byte{0x5, 0x0}); err != nil {
		return nil, common.NewError("failed to respond auth").Base(err)
	}

	buf := [3]byte{}
	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		return nil, common.NewError("failed to read command").Base(err)
	}

	addr := new(tunnel.Address)
	_, err := addr.ReadFrom(conn)
	if err != nil {
		return nil, err
	}

	return &Conn{
		metadata: &tunnel.Metadata{
			Command: tunnel.Command(buf[1]),
			Address: addr,
		},
		Conn: conn,
	}, nil
}

func (s *Server) connect(conn net.Conn) error {
	_, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}

func (s *Server) associate(conn net.Conn, addr *tunnel.Address) error {
	// RFC 1928：UDP ASSOCIATE 响应中的 BND.ADDR/BND.PORT 是服务端 UDP 中继地址，
	// 客户端随后向该地址发送 UDP 报文。标准 socks5 客户端（如 txthinking/socks5）
	// 会原样使用该地址，回显请求的 DST（通常为 0.0.0.0:0）会导致 UDP 关联永远无法建立。
	udpAddr, ok := s.listenPacketConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return common.NewError("socks failed to get udp relay address")
	}
	relayAddr := tunnel.NewAddressFromHostPort("udp", udpAddr.IP.String(), udpAddr.Port)
	buf := bytes.NewBuffer([]byte{0x05, 0x00, 0x00})
	_, err := relayAddr.WriteTo(buf)
	if err != nil {
		return common.NewError("socks failed to write relay address").Base(err)
	}
	_, err = conn.Write(buf.Bytes())
	return err
}

// watchAssociation 接管一条已经回完 ASSOCIATE 响应的控制连接：读到对端关闭为止，
// 然后回收这个 fd。读循环阻塞在 conn.Read 上、不看 ctx，所以连接登记在
// associations 里，由 Close 显式关掉来唤醒——否则 Close 的 wg.Wait 挂死。
func (s *Server) watchAssociation(conn tunnel.Conn) {
	// 握手期设置的截止时间还挂着，必须解除：这条连接要靠对端 FIN 或
	// associationMaxLife 结束，不能被 30s 的 handshakeTimeout 掐断
	conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：失败时由 associationMaxLife 兜底

	s.assocLock.Lock()
	if s.ctx.Err() != nil {
		// 与 Close 的排空竞争：对方已经收完登记表了，这里不能再塞进去
		s.assocLock.Unlock()
		conn.Close() //gosec:disable -- 错误忽略：关停路径，尽力关闭
		return
	}
	if s.associations == nil {
		s.associations = make(map[tunnel.Conn]struct{})
	}
	s.associations[conn] = struct{}{}
	s.assocLock.Unlock()

	s.wg.Go(func() {
		defer func() {
			s.assocLock.Lock()
			delete(s.associations, conn)
			s.assocLock.Unlock()
			conn.Close() //gosec:disable -- 错误忽略：这里就是要无条件回收
		}()
		// 硬上限到点直接关闭，阻塞中的读循环会因此报错退出
		killer := time.AfterFunc(associationMaxLife, func() { _ = conn.Close() })
		defer killer.Stop()
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				log.Debug("socks association control connection ended")
				return
			}
		}
	})
}

// closeAssociations 关掉所有在途关联的控制连接，唤醒阻塞中的读循环。
func (s *Server) closeAssociations() {
	s.assocLock.Lock()
	conns := make([]tunnel.Conn, 0, len(s.associations))
	for c := range s.associations {
		conns = append(conns, c)
	}
	s.associations = nil
	s.assocLock.Unlock()
	for _, c := range conns {
		c.Close() //gosec:disable -- 错误忽略：关停路径，尽力关闭
	}
}

func (s *Server) packetDispatchLoop() {
	buf := make([]byte, MaxPacketSize)
	for {
		n, src, err := s.listenPacketConn.ReadFrom(buf)
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 可能误走 continue 变成 busy loop；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			continue
		}
		log.Debug("socks recv udp packet from", src)
		s.mappingLock.RLock()
		conn, found := s.mapping[src.String()]
		s.mappingLock.RUnlock()
		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			conn = &PacketConn{
				input:      make(chan *packetInfo, 128),
				output:     make(chan *packetInfo, 128),
				ctx:        ctx,
				cancel:     cancel,
				PacketConn: s.listenPacketConn,
				src:        src,
			}
			// 必须先登记再启协程。协程可能立刻走超时或 ctx.Done 分支去 delete，
			// 那时 delete 打空，随后的插入就留下一个永不释放的 stale 条目：
			// 既占着内存，又让该源 IP 之后永远命中这个死会话、再也建不起 UDP
			s.mappingLock.Lock()
			s.mapping[src.String()] = conn
			s.mappingLock.Unlock()

			s.wg.Go(func() {
				defer conn.Close()
				// UDP 会话空闲超时与 dokodemo/tproxy 对齐,取配置的 UDPTimeout(默认 60s)
				timeout := s.timeout
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				responseBuf := make([]byte, MaxPacketSize)
				for {
					select {
					case info := <-conn.output:
						// 直接在会话自己那块 responseBuf 上就地拼：走 Address.AppendTo 而不是
						// Address.WriteTo，后者每包还要再分配一个临时切片才能喂给 io.Writer
						out := responseBuf[:0]
						out = append(out, 0, 0, 0) // RSV, FRAG
						var err error
						out, err = info.metadata.Address.AppendTo(out)
						if err != nil {
							log.Error("socks failed to write address")
							return
						}
						out = append(out, info.payload...)
						_, err = s.listenPacketConn.WriteTo(out, conn.src)
						if err != nil {
							log.Error("socks failed to respond packet to", conn.src)
							return
						}
						log.Debug("socks respond udp packet to", conn.src, "metadata", info.metadata)
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(timeout)
					case <-timer.C:
						log.Info("socks udp session timeout, closed")
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						return
					case <-conn.ctx.Done():
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						log.Info("socks udp session closed")
						return
					}
				}
			})

			select {
			case s.packetChan <- conn:
				log.Info("socks new udp session from", src)
			case <-s.ctx.Done():
				// 下游已停止消费，必须退出，否则关闭时该 goroutine 永久阻塞导致泄露
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				log.Debug("exiting")
				return
			}
		}
		r := bytes.NewReader(buf[3:n])
		address := new(tunnel.Address)
		_, err = address.ReadFrom(r)
		if err != nil {
			log.Error(common.NewError("socks failed to parse incoming packet").Base(err))
			continue
		}
		remaining := r.Len()
		if remaining <= 0 {
			continue
		}
		payload := make([]byte, remaining)
		_, _ = r.Read(payload)
		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: address,
			},
			payload: payload,
		}:
		default:
		}
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	ctx, cancel := context.WithCancel(ctx)
	listenPacketConn, err := underlay.AcceptPacket(nil)
	if err != nil {
		cancel()
		return nil, common.NewError("socks failed to accept packet conn").Base(err)
	}
	server := &Server{
		underlay:         underlay,
		localHost:        cfg.LocalHost,
		localPort:        cfg.LocalPort,
		timeout:          time.Duration(cfg.UDPTimeout) * time.Second,
		connChan:         make(chan tunnel.Conn, 32),
		packetChan:       make(chan tunnel.PacketConn, 32),
		mapping:          make(map[string]*PacketConn),
		associations:     make(map[tunnel.Conn]struct{}),
		ctx:              ctx,
		cancel:           cancel,
		listenPacketConn: listenPacketConn,
	}
	server.wg.Go(func() {
		server.acceptConnLoop()
	})
	server.wg.Go(func() {
		server.packetDispatchLoop()
	})
	return server, nil
}
