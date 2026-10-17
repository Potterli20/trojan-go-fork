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

	// udpSessionQueueSize 是每个 UDP 会话收发队列的深度，与 dokodemo 的 16 对齐。
	// 旧的 128 配上最大 8KB 的负载，两条通道就是每会话 2MB 的驻留上限，而会话表
	// 只按空闲超时清理——一个源端口密集的客户端能把这份占用乘上会话数堆上去。
	// 队列满时投递侧走 select/default 丢包：UDP 语义下丢包优于按会话无界占内存。
	udpSessionQueueSize = 16

	// maxUDPSessions 是会话表条目数的硬上限。mapping 只有空闲超时，条目数等于
	// UDPTimeout × 新源地址到达速率，而源地址是可以伪造的：每条会话攥着两条深度 16
	// 的通道、一个子 ctx 和一个守护协程。口径与 tproxy 一致（那边 2026-10-16 先补，
	// 判据见 tunnel/tproxy/session_bound_test.go）。
	maxUDPSessions = 4096

	// maxAssociations 是在途 ASSOCIATE 控制连接条目数的硬上限，与 maxUDPSessions 同值。
	// associations 过去只有 associationMaxLife 一个时限，条目数 = 发呆对端的到达速率 ×
	// 600s，而这条 TCP 在握手阶段没有任何认证；每条登记攥着 1 个 fd、1 个守护协程、
	// 1 个 AfterFunc 计时器和 1 个 map 条目。取同一个数的理由是控制连接再多也开不出
	// 更多 UDP 会话（会话表已有同值闸），超出部分只会白占 fd——正是 mapping 那条
	// 「新增同类表照这个形态抄」的不变量漏掉的姊妹表。
	maxAssociations = maxUDPSessions

	// responseFrameCap 是会话守护协程那块回包组帧缓冲的容量：一帧最坏是
	// 「RSV+FRAG(3) + 地址块(ATYP(1)+长度字节(1)+域名(255)+端口(2)=259) + 负载(≤MaxPacketSize)」，
	// 取整到 +264。只留 MaxPacketSize 的话满尺寸负载必然把 append 撑到界外——每包新造
	// 一份 10880（实测的 Go size class），而缓冲本身永远停在 8192，下一包再撑一次；
	// 远端只要按线上格式合法发满尺寸 datagram 就能逐包加压本地 GC。
	// 口径同 tunnel/trojan/packet.go 的 packetBufCap。
	responseFrameCap = MaxPacketSize + 264
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
	mapping          map[tunnel.SessionKey]*PacketConn
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
	// ASSOCIATE 被容量闸拒掉的次数。对端只要不停地建连+握手就能按自己的速率顶这条
	// 日志，形态与 packetDispatchLoop 的读错误限流一致：每 10 次才打一条 Error。
	assocRefused := 0
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
				// 先登记再接管：登记里带容量闸（见 maxAssociations），超限或本层已关停时
				// 这条连接还没回过任何字节，所以这里改得动口——回一条失败帧而不是"先答应
				// 再默默断掉"。接管之后这条 TCP 连接按 RFC 就是 UDP 关联的生命周期凭据，
				// 既不能交给下游也不能就此弃用。
				if err := s.watchAssociation(handledConn); err != nil {
					assocRefused++
					if assocRefused%10 == 0 {
						log.Error(common.NewError("socks associate refused").Base(err))
					} else {
						log.Debug(common.NewError("socks associate refused").Base(err))
					}
					declineAssociate(handledConn)
					handledConn.Close() //gosec:disable -- 错误忽略：拒绝路径，登记没成功就没有人替它回收
					return
				}
				err = s.associate(handledConn, handledConn.Metadata().Address)
				if err != nil {
					log.Error(common.NewError("socks failed to respond associate").Base(err))
					// 守护协程已经攥着这条连接，Close 会让它的读循环报错退出并注销登记
					handledConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
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
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已完成握手但没被
	// 上层取走"的连接。留在缓冲里的每一条都攥着一个 fd（已完成握手的客户端 TCP 连接），
	// 不关就只能等进程退出，反复建栈（api、测试、热重载）时是实打实的堆积。
drain:
	for {
		select {
		case c := <-s.connChan:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drain
		}
	}
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已完成握手但没被
	// 上层取走"的连接。留在缓冲里的每一条都攥着一个 fd（UDP 会话（含两条通道与在途包）），
	// 不关就只能等进程退出，反复建栈（api、测试、热重载）时是实打实的堆积。
drainPacket:
	for {
		select {
		case c := <-s.packetChan:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drainPacket
		}
	}
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
	// 跳过 method 列表。不用 io.CopyN：Go 1.27 的 io.copyBuffer 在 buf==nil 时
	// 直接 make([]byte, 32*1024)，而这里是**每条 TCP 连接一次的握手**路径、
	// 且在认证之前——未认证的对端只要不断建连，就能按连接数放大本端分配。
	// NMETHODS 是 1 字节，上限 255，一块栈上数组就够。
	if n := nmethods[0]; n > 0 {
		var skip [255]byte
		if _, err := io.ReadFull(conn, skip[:n]); err != nil {
			return nil, common.NewError("socks failed to read methods").Base(err)
		}
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
	// 手里已经有 *net.UDPAddr，不必拼成字符串再解析一遍（口径同 6857b3f2 那批）。
	// 帧也用 AppendTo 就地攒：bytes.NewBuffer([]byte{...}) 会把外部切片直接交给 buffer
	// 持有，配上 WriteTo 就是全仓最后一处"旧地址序列化 + 传参别名"的形态。
	relayAddr, err := tunnel.NewAddressFromNetAddr("udp", udpAddr)
	if err != nil {
		return common.NewError("socks failed to build udp relay address").Base(err)
	}
	// 预留 3+19：IPv6 的地址块（ATYP+16 地址+2 端口）是这里的上限，一次写完不扩容
	frame := make([]byte, 0, 3+19)
	frame = append(frame, 0x05, 0x00, 0x00)
	frame, err = relayAddr.AppendTo(frame)
	if err != nil {
		return common.NewError("socks failed to write relay address").Base(err)
	}
	_, err = conn.Write(frame)
	return err
}

// declineAssociate 回一条 SOCKS5 失败响应（REP=0x01 通用服务端失败）供被容量闸拒掉的
// ASSOCIATE 使用。不能只写两字节就断：标准客户端会按 10 字节的定长回复解析 BND.ADDR/
// BND.PORT，短回复会被当成协议错误；也让客户端明确知道是"这台服务器拒绝了关联"，
// 而不是"答应了又莫名断开"。
func declineAssociate(conn net.Conn) {
	_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

// watchAssociation 接管一条已经收到 ASSOCIATE 请求的控制连接：登记进 associations、
// 读到对端关闭为止，然后回收这个 fd。读循环阻塞在 conn.Read 上、不看 ctx，所以连接
// 登记在 associations 里，由 Close 显式关掉来唤醒——否则 Close 的 wg.Wait 挂死。
//
// 容量检查与登记在同一把写锁里完成，并且调用点在**回成功响应之前**：登记失败(超限或
// 已关停)时这条连接一个字都还没回，上游可以直接改回失败帧。返回非 nil 时调用方必须
// 负责关闭 conn——登记没成功就没有守护协程，也就没人会替它注销。
func (s *Server) watchAssociation(conn tunnel.Conn) error {
	// 握手期设置的截止时间还挂着，必须解除：这条连接要靠对端 FIN 或
	// associationMaxLife 结束，不能被 30s 的 handshakeTimeout 掐断
	conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：失败时由 associationMaxLife 兜底

	s.assocLock.Lock()
	if s.ctx.Err() != nil {
		// 与 Close 的排空竞争：对方已经收完登记表了，这里不能再塞进去
		s.assocLock.Unlock()
		return common.NewError("socks server closed")
	}
	if len(s.associations) >= maxAssociations {
		s.assocLock.Unlock()
		return common.NewErrorf("socks association limit %d reached", maxAssociations)
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
	return nil
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
	readErrors := 0
	limitDrops := 0 // 会话上限丢弃计数，只用于日志限流
	for {
		n, src, err := s.listenPacketConn.ReadFrom(buf)
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 可能误走 continue 变成 busy loop；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			// 错误退避。旧写法这里直接 continue，而 socket 一旦进入持续报错状态（下层
			// PacketConn 被关掉一半、内核错误码粘住）就是一整条满 CPU 的热循环，每轮还打
			// 一条日志。socks 的 listenPacketConn 来自 underlay.AcceptPacket，本层没有权利
			// 像 tproxy 那样自杀式关监听（那不是我们的 fd），所以只做退避 + 日志限流。
			readErrors++
			if readErrors%10 == 0 {
				log.Error(common.NewError("socks udp socket persistently failing").Base(err))
			} else {
				log.Debug(common.NewError("socks udp read error").Base(err))
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(time.Millisecond * 100):
			}
			continue
		}
		readErrors = 0
		// SOCKS5 UDP 请求 = RSV(2) + FRAG(1) + ATYP(1) + 地址 + 端口。短于"分片头 + ATYP"
		// 这 4 字节必须立刻丢掉，而且要卡在**建会话之前**：下面 `bytes.NewReader(buf[3:n])`
		// 在 n<3 时是切片越界 panic，而这里的对端是未认证的 UDP 源——一个零长度数据报就能
		// 把整个进程打崩（relay 这条链上没有 recover）；n=3 的包虽然不 panic，但会先建出一条
		// 永远投递不出负载的会话。口径同 tunnel/freedom/conn.go 的同型闸门。
		if n < 4 {
			log.Debug("socks dropping short udp packet from", src.String())
			continue
		}
		log.Debug("socks recv udp packet from", src)
		// 键归一放在 tunnel.SessionKey：语义与 addr.String() 一致（v4-mapped-v6 归一、
		// zone 保留），但这条是**每包一次**的路径，不必再格式化字符串。
		srcKey := tunnel.NewSessionKey(src)
		s.mappingLock.RLock()
		conn, found := s.mapping[srcKey]
		s.mappingLock.RUnlock()
		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			// 容量检查与登记在同一把写锁里，并且卡在**造会话对象之前**：会话表条目数
			// 原本只由空闲超时与新源地址到达速率决定，而源地址可伪造（见 maxUDPSessions）。
			s.mappingLock.Lock()
			if len(s.mapping) >= maxUDPSessions {
				s.mappingLock.Unlock()
				cancel()
				// 限流：到上限之后这条路径的速率就等于**新源地址到达速率**，而源地址可
				// 伪造；src.String() 还必须算在 ShouldLog 之后，否则默认级别下每丢一包
				// 照样付一次格式化 + 整条脱敏链（口径同下面的读错误限流与 AGENTS.md）。
				limitDrops++
				if limitDrops%10 == 0 {
					log.Error("socks udp session limit reached, dropping packet from", src.String())
				} else if log.ShouldLog(log.DebugLevel) {
					log.Debug("socks udp session limit reached, dropping packet from", src.String())
				}
				continue
			}
			conn = &PacketConn{
				input:      make(chan *packetInfo, udpSessionQueueSize),
				output:     make(chan *packetInfo, udpSessionQueueSize),
				ctx:        ctx,
				cancel:     cancel,
				PacketConn: s.listenPacketConn,
				src:        src,
			}
			// 必须先登记再启协程。协程可能立刻走超时或 ctx.Done 分支去 delete，
			// 那时 delete 打空，随后的插入就留下一个永不释放的 stale 条目：
			// 既占着内存，又让该源 IP 之后永远命中这个死会话、再也建不起 UDP
			s.mapping[srcKey] = conn
			s.mappingLock.Unlock()

			s.wg.Go(func() {
				// 无论会话因超时、ctx 还是写失败退出，都必须移除表项并关闭会话。
				// 旧写法把 delete 只写在 timer.C 与 ctx.Done 两个分支里，而下面
				// 序列化失败和 WriteTo 失败这两条 return 绕过了它们：表项连同
				// input/output 两条通道永久滞留，且该源地址之后的每个包都因为
				// found 恒真被投进这个没人读的死会话（select/default 丢弃），
				// 同一个源端口重开会话就是彻底黑洞——只能等满 UDPTimeout 才自愈。
				// dokodemo/tproxy 的同型 daemon 已改成 defer 统一回收，这里对齐。
				defer func() {
					s.mappingLock.Lock()
					delete(s.mapping, tunnel.NewSessionKey(conn.src))
					s.mappingLock.Unlock()
					conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				}()
				// UDP 会话空闲超时与 dokodemo/tproxy 对齐,取配置的 UDPTimeout(默认 60s)
				timeout := s.timeout
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				responseBuf := make([]byte, responseFrameCap)
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
							// 手上这一条不在任何队列里，Close 的排空够不着它
							putPacketBuf(info.payload)
							return
						}
						out = append(out, info.payload...)
						_, err = s.listenPacketConn.WriteTo(out, conn.src)
						putPacketBuf(info.payload)
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
						return
					case <-conn.ctx.Done():
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
		// 从池里借一块装这个包的负载；借用权随 packetInfo 交给会话，
		// 最后由 ReadWithMetadata 归还（或会话被拆时由 Close 的排空归还）。
		// remaining 已经过 SOCKS 帧解析，恒 <= MaxPacketSize - 帧头长度。
		payload := getPacketBuf()
		_, _ = r.Read(payload[:remaining])
		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: address,
			},
			payload: payload[:remaining],
		}:
		default:
			// 会话可能刚超时清理、已无读者：丢包优于阻塞 dispatch 循环，
			// 但丢包的同时必须把借出的负载还回去，否则每次投递失败漏一块 8KB
			putPacketBuf(payload)
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
		mapping:          make(map[tunnel.SessionKey]*PacketConn),
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
