package http

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// handshakeTimeout 限定等待单个 HTTP 请求 (含 keep-alive 后续请求) 的时限
const handshakeTimeout = 30 * time.Second

// maxRequestHeadBytes 限定单个请求头块可读取的字节数，取 net/http 的
// DefaultMaxHeaderBytes 同档（1MB）。stdlib 的 http.ReadRequest 自身没有这个上限
// ——有上限的是内部 readRequestLimit，只服务 Server；transport/tls/websocket 各层
// 嗅探时都做了限幅，此前只有本文件这个真实代理入口漏了。
const maxRequestHeadBytes = 1 << 20

// resolveProxyTarget 把请求里的目标信息解析成转发地址：带端口的直接用；
// 只有主机名（`Host: example.com`，代理语义下合法）按默认端口 80。
//
// 但**完全没有目标信息**必须拒绝（HTTP/1.0 不带 Host，或 h2 请求缺 :authority）。
// 让它退化成空主机名会一路走到 net.Dial("tcp", ":80")，实测那是**连接 127.0.0.1**
// ——等于代理替请求方去访问自己的本地服务，还会绕过按域名匹配的路由规则
// （域名是空串时谁都匹配不上，直接落到默认策略）。
func resolveProxyTarget(host string) (*tunnel.Address, error) {
	if host == "" {
		return nil, common.NewError("http proxy request has no target host")
	}
	if addr, err := tunnel.NewAddressFromAddr("tcp", host); err == nil {
		return addr, nil
	}
	return tunnel.NewAddressFromHostPort("tcp", host, 80), nil
}

type ConnectConn struct {
	net.Conn
	metadata *tunnel.Metadata
}

func (c *ConnectConn) Metadata() *tunnel.Metadata {
	return c.metadata
}

type OtherConn struct {
	net.Conn
	metadata   *tunnel.Metadata // fixed
	reqReader  *io.PipeReader
	reqWriter  *io.PipeWriter
	respWriter *io.PipeWriter
	ctx        context.Context
	cancel     context.CancelFunc
}

func (c *OtherConn) Metadata() *tunnel.Metadata {
	return c.metadata
}

func (c *OtherConn) Read(p []byte) (int, error) {
	n, err := c.reqReader.Read(p)
	if err == io.EOF {
		if n != 0 {
			// io.Pipe 不应产生 n>0 且 EOF;防御此死分支时不杀进程
			return n, io.ErrUnexpectedEOF
		}
		if c.ctx.Err() != nil {
			return 0, common.NewError("http conn closed")
		}
		return 0, io.EOF
	}
	return n, err
}

func (c *OtherConn) Write(p []byte) (int, error) {
	return c.respWriter.Write(p)
}

// Close 结束「这一个请求」的转发会话：关掉管道两端，让两个转发方向都从阻塞中
// 报错返回。reqWriter 也必须在这里关——请求写完就发 EOF 会让 relay 误判会话结束
// 并把还在路上的响应砍断（见 acceptLoop 里的说明）。
// 注意它刻意不关裸连接：裸连接由 handler 拥有并支持 keep-alive。
func (c *OtherConn) Close() error {
	c.cancel()
	c.reqReader.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	c.reqWriter.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	c.respWriter.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	return nil
}

type Server struct {
	underlay tunnel.Server
	connChan chan tunnel.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Error(common.NewError("http closed"))
				return
			}
			log.Error(common.NewError("http failed to accept connection").Base(err))
			continue
		}

		s.wg.Go(func() {
			// 预算读取器挂在 conn 与 bufio 之间：每个请求头单独计预算，解析成功后立刻
			// 解除限制，这样请求体和 keep-alive 的后续请求都不受影响。不能把
			// io.LimitReader 直接包在 conn 外面——那限制的是整条连接的累计字节数，
			// 长连接上第 N 个请求之后就会被截断。
			boundedReader := common.NewBoundedReader(conn)
			reqBufReader := bufio.NewReader(boundedReader)
			// 等待首个请求限时:对端静默 (如端口扫描) 时不让 handler 永久阻塞
			conn.SetReadDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			// 先分一次流：一条连接只会是 h1 或 h2c 之一，所以嗅到 HTTP/2 前文就整条
			// 交给 serveH2Conn（内部按流并发），不再走下面的 h1 循环。
			// 放在头部预算之前是有原因的：这一支根本不做 h1 头部解析，夹在
			// Limit/Unlimited 中间会让"每个 ReadRequest 调用点成对使用预算"这条
			// 不变量失去意义（TestEveryReadRequestSiteIsBounded 就是抓这个的）。
			// 嗅探本身最多只触发一次 bufio 填充（4KiB），不是无界读取。
			if head, perr := reqBufReader.Peek(h2PrefaceProbe); perr == nil && string(head) == "PRI *" {
				s.serveH2Conn(conn, reqBufReader)
				return
			}
			boundedReader.Limit(maxRequestHeadBytes)
			req, err := http.ReadRequest(reqBufReader)
			boundedReader.Unlimited()
			conn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			if err != nil {
				log.Error(common.NewError("not a valid http request").Base(err))
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				return
			}

			if strings.ToUpper(req.Method) == "CONNECT" { // CONNECT
				addr, err := tunnel.NewAddressFromAddr("tcp", req.Host)
				if err != nil {
					log.Error(common.NewError("invalid http dest address").Base(err))
					req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					conn.Close()     //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
				resp := fmt.Sprintf("HTTP/%d.%d 200 Connection established\r\n\r\n", req.ProtoMajor, req.ProtoMinor)
				_, err = conn.Write([]byte(resp))
				if err != nil {
					log.Error("http failed to respond connect request")
					req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					conn.Close()     //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
				req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				connectConn := &ConnectConn{
					Conn: conn,
					metadata: &tunnel.Metadata{
						Address: addr,
					},
				}
				select {
				case s.connChan <- connectConn:
				case <-s.ctx.Done():
					// 下游已停止消费，关闭连接并返回，否则 goroutine 永久阻塞
					connectConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					return
				}
			} else { // GET, POST, PUT...
				// 这条裸连接由本 goroutine 独占（转发读写都走 io.Pipe，不碰它），
				// 所以关闭统一经由 closeRawConn，保证一次连接只关一次。
				closeRawConn := sync.OnceFunc(func() {
					conn.Close() //gosec:disable -- 错误忽略：关停唤醒路径，错误无处可报
				})
				defer closeRawConn()
				// connDone 是"这个 handler 已经返回"的信号。watcher 只监听 s.ctx 的话，
				// 它要一直 park 到整个 Server.Close：每条连接留下一个 goroutine，而且它
				// 经由这个闭包留住裸连接对象（TLS/websocket 的读写缓冲都在里面），
				// 连接数一多就线性堆积、运行期永不回收。对端用"一个请求一条连接"
				// （Connection: close、curl、扫描器）时最容易撞上。
				connDone := make(chan struct{})
				defer close(connDone)
				// 关停唤醒：handler 可能停在「把响应写回客户端」上——对端不再读取时
				// 那一句会无限阻塞；也可能停在 keep-alive 的下一个请求读上（要等满
				// handshakeTimeout=30s）。两种都会让 Close 里的 wg.Wait 收不了尾，只能靠
				// Proxy.Close 的 5s 兜底掐断——实测空闲 keep-alive 场景关停正好 5.001s。
				// 所以 s.ctx 取消时直接关掉裸连接，让在途读写立刻报错返回（修复后约 1ms）。
				// 裸连接仍然只在 s.ctx 取消时才关：单个请求结束时的 newConn.Close()
				// 只关管道，绝不能关裸连接，否则 keep-alive 退化成一条连接只服务一个请求。
				// 这里的 wg.Go 是合法的：本 goroutine 自身还占着一个计数，所以 Add 时
				// 计数必然 >0，Close 的 Wait 也必然等到这个 watcher 退出。
				s.wg.Go(func() {
					select {
					case <-s.ctx.Done():
						closeRawConn()
					case <-connDone:
						// handler 自己走完了（正常结束或报错），不再需要这个唤醒者
					}
				})
				for {
					reqReader, reqWriter := io.Pipe()
					respReader, respWriter := io.Pipe()
					var addr *tunnel.Address
					addr, err = resolveProxyTarget(req.Host)
					if err != nil {
						log.Error(err)
						// 裸连接由 defer closeRawConn 收，这里只需回一个 400
						conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")) //gosec:disable -- 错误忽略：对端可能已经断开，无处可报
						req.Body.Close()                                                                                 //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						return
					}
					log.Debug("http dest", addr)

					ctx, cancel := context.WithCancel(s.ctx)
					newConn := &OtherConn{
						Conn: conn,
						metadata: &tunnel.Metadata{
							Address: addr,
						},
						ctx:        ctx,
						cancel:     cancel,
						reqReader:  reqReader,
						reqWriter:  reqWriter,
						respWriter: respWriter,
					}

					select {
					case s.connChan <- newConn:
					case <-s.ctx.Done():
						newConn.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						return
					}

					// reqWriter 不能在这里关：本仓库的 relay 是「任一方向结束就拆掉整个
					// 会话」，而 HTTP 请求写完必然 EOF —— 提前 EOF 会让响应在传输途中被
					// 砍断（实测 64KB 响应只过去了 4KB）。会话的结束统一由 newConn.Close()
					// 表示，所以每个出口都必须关掉它，否则两个转发方向会永远阻塞在管道上。
					err = req.Write(reqWriter)
					if err != nil {
						log.Error(common.NewError("http failed to write http request").Base(err))
						newConn.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						return
					}

					respBufReader := bufio.NewReader(io.NopCloser(respReader))
					resp, err := http.ReadResponse(respBufReader, req)
					if err != nil {
						log.Error(common.NewError("http failed to read http response").Base(err))
						newConn.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						req.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						return
					}
					err = resp.Write(conn)
					if err != nil {
						log.Error(common.NewError("http failed to write the response back").Base(err))
						newConn.Close()   //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						req.Body.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						resp.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						return
					}
					newConn.Close()   //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					req.Body.Close()  //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					resp.Body.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理

					// keep-alive:等待下一个请求同样限时，读完即解除
					conn.SetReadDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					boundedReader.Limit(maxRequestHeadBytes)
					req, err = http.ReadRequest(reqBufReader)
					boundedReader.Unlimited()
					conn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
					if err != nil {
						log.Error(common.NewError("http failed to read request from local").Base(err))
						return
					}
				}
			}
		})
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("http server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	<-s.ctx.Done()
	return nil, common.NewError("http server closed")
}

func (s *Server) Close() error {
	s.cancel()
	err := s.underlay.Close()
	s.wg.Wait()
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已完成握手但没被
	// 上层取走"的连接。留在缓冲里的每一条都攥着一个 fd（CONNECT 之后已经双向打通的裸 TCP 连接），
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
	return err
}

// NewServer 创建 HTTP 入站服务器。
// 这里刻意不读 ctx 里的模块配置：HTTP 从未注册 config creator，FromContext 恒为 nil，
// 曾经对它做裸类型断言会让所有含 http 入站的 client 模式在建栈阶段直接 panic。
func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		underlay: underlay,
		connChan: make(chan tunnel.Conn, 32),
		ctx:      ctx,
		cancel:   cancel,
	}
	server.wg.Go(func() {
		server.acceptLoop()
	})
	return server, nil
}
