package http

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// h2PrefaceProbe 是嗅探 HTTP/2 前文需要先看的最小字节数。
//
// 取 5（"PRI *"）而不是整个 24 字节前文：bufio.Peek 必须攒够才返回，而一条 h1
// 的首个请求完全可能短于 24 字节（"GET / HTTP/1.1\r\n\r\n" 只有 18 字节），
// 按 24 去 Peek 会让合法的小请求白等一个读超时。"PRI *" 不是合法的 h1 请求行
// （方法后紧跟裸 "*" 作为 request-target 不成立），所以 5 字节足够判真。
const h2PrefaceProbe = 5

// h2CopyBufSize 取 32KB，与 io.copyBuffer 在 buf==nil 时的默认值一致：Go 1.27 起
// io 内部已经不再有复用池（src/io/io.go:418-426 直接 `make([]byte, size)`），
// 而这里的 dst（http2 的 ResponseWriter）不实现 ReaderFrom、src（resp.Body）不实现
// WriterTo，走的就是这条分配路径 —— 每条被代理的 h2c 请求都要新造再丢一块 32KB。
// 换成池化只改分配来源，吞吐与字节序都不变。
const h2CopyBufSize = 32 * 1024

var h2CopyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, h2CopyBufSize)
		return &buf
	},
}

func getH2CopyBuf() []byte { return *h2CopyBufPool.Get().(*[]byte) }

func putH2CopyBuf(buf []byte) {
	// 与 redirector.copyBufPool / proxy.boundedBufPool 同理：尺寸不符就交给 GC，
	// 不让一次异常占用把整个池的水位顶高
	if len(buf) != h2CopyBufSize {
		return
	}
	h2CopyBufPool.Put(&buf)
}

// bufferedConn 把裸连接与"已经读过一部分"的 bufio.Reader 合成一个 net.Conn 交给
// http2.ServeConn。必须这样接：嗅探用的字节（往往还不止前文，SETTINGS 帧通常已经
// 一起进了 bufio 的缓冲）已经从 socket 里读走了，直接给 ServeConn 裸连接会让它
// 看不全前文、或把已缓冲的帧当成乱码。
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// h2StreamAddr 只提供地址信息。HTTP/2 的一条流不是 net.Conn，读写都走 io.Pipe
// （OtherConn 已经覆写了 Read/Write/Close），所以这里只满足接口，
// 其余方法明确报错而不是 panic，免得将来有人误用。
type h2StreamAddr struct{ remote net.Addr }

var errH2StreamNotAConn = common.NewError("an HTTP/2 stream is not a byte-level connection; use the relay pipes")

func (h *h2StreamAddr) Read([]byte) (int, error)    { return 0, errH2StreamNotAConn }
func (h *h2StreamAddr) Write([]byte) (int, error)   { return 0, errH2StreamNotAConn }
func (h *h2StreamAddr) Close() error                { return nil }
func (h *h2StreamAddr) LocalAddr() net.Addr         { return h.remote }
func (h *h2StreamAddr) RemoteAddr() net.Addr        { return h.remote }
func (h *h2StreamAddr) SetDeadline(time.Time) error { return nil }
func (h *h2StreamAddr) SetReadDeadline(time.Time) error {
	return nil
}
func (h *h2StreamAddr) SetWriteDeadline(time.Time) error { return nil }

// serveH2Conn 接管一条已确认发出 HTTP/2 前文的入站连接（h2c，先前知识）。
//
// 能力边界要讲清楚：Go 的 x/net/http2 服务端没有实现 http.Hijacker（整个包里
// 零命中），所以 h2 上的 CONNECT 桥不成裸隧道 —— 这类请求显式回 501。
// 绝对 URI 形态的代理请求（GET/POST/...，即浏览器对明文代理开 proxy-http2 时
// 发的那一种）完整支持：每条流转成一个转发会话，走的还是 h1 分支那套
// io.Pipe + OtherConn，因此 trojan / simplesocks 等上层看到的形状不变。
func (s *Server) serveH2Conn(conn tunnel.Conn, br *bufio.Reader) {
	// 首请求的读超时是给 h1 用的；h2 连接要长期活着（空闲期靠帧保活），
	// 不清掉的话每条闲置连接都会在 30s 后被自己的 deadline 掐断。
	conn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：底层连接不支持时由 ServeConn 自己限时

	// 关停唤醒，和 h1 分支同一件事、同一个理由。ServeConn 的契约是「阻塞到连接
	// 不可读为止」：这一版 http2 的 serverConn 里没有任何 ctx.Done() 监听（全文件
	// 零命中），所以 opts.Context 只影响各请求自己的 ctx，掐不断那个读帧循环。
	// 少了这个 watcher，Server.Close 的 wg.Wait 会永远等在 ServeConn 里——实测
	// 测试清理直接卡到 go test 超时。
	// 这里的 wg.Go 合法：调用方本身占着一个计数（acceptLoop 里的 s.wg.Go）。
	closeRawConn := sync.OnceFunc(func() {
		conn.Close() //gosec:disable -- 错误忽略：关停唤醒路径，错误无处可报
	})
	defer closeRawConn()
	s.wg.Go(func() {
		<-s.ctx.Done()
		closeRawConn()
	})

	hs := &http2.Server{
		MaxConcurrentStreams: 128,
		MaxReadFrameSize:     1 << 20,
	}
	opts := &http2.ServeConnOpts{Handler: &h2Handler{srv: s, conn: conn}, Context: s.ctx}
	// Context 挂 s.ctx：关停时各条流的请求 ctx 随之取消，在途流的读写会报错返回，
	// 上面的 closeRawConn 则负责把还堵在读帧上的连接主循环放倒。
	// ServeConn 无返回值，契约是「阻塞到这条连接不可读」——它只看连接本身，不会
	// 因为 ctx 取消而返回，所以上面那个 watcher 不是可选项。
	hs.ServeConn(&bufferedConn{Conn: conn, r: br}, opts)
}

type h2Handler struct {
	srv  *Server
	conn tunnel.Conn
}

func (h *h2Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// 见上面的能力边界：没有 Hijack 就桥不出裸隧道，显式拒绝比假装支持安全。
		http.Error(w, "CONNECT over HTTP/2 is not supported; use HTTP/1.1 for CONNECT",
			http.StatusNotImplemented)
		return
	}

	// 目的地来自 :authority，语义等价于 h1 的绝对形式 / Host，
	// 并且必须走同一个解析入口：空主机名同样会退化成"拨本机"。
	addr, err := resolveProxyTarget(r.Host)
	if err != nil {
		log.Debug("http2 request without a target: ", err)
		http.Error(w, "missing target host", http.StatusBadRequest)
		return
	}
	log.Debug("http2 dest", addr)

	reqReader, reqWriter := io.Pipe()
	respReader, respWriter := io.Pipe()
	ctx, cancel := context.WithCancel(h.srv.ctx)
	stream := &OtherConn{
		Conn:       &h2StreamAddr{remote: h.conn.RemoteAddr()},
		metadata:   &tunnel.Metadata{Address: addr},
		ctx:        ctx,
		cancel:     cancel,
		reqReader:  reqReader,
		reqWriter:  reqWriter,
		respWriter: respWriter,
	}
	// 无论走到哪个出口，本条流的会话都必须收掉：relay 侧靠 Close 解除阻塞。
	defer stream.Close()

	// 请求方向的 EOF 由上面的 defer（OtherConn.Close 会关 reqWriter）发出，
	// 与 h1 分支同一条约束：提前 EOF 会让"任一方向结束就拆会话"的 relay
	// 把还在路上的响应砍断。
	select {
	case h.srv.connChan <- stream:
	case <-h.srv.ctx.Done():
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	// 请求体是流式的，可能要一直推到响应回来为止，所以另起一个 goroutine。
	//
	// 这里刻意不调 EnableFullDuplex：实测在本版（x/net v0.61.0 的 ServeConn 直接
	// 委派给 stdlib 的 internal/http2）去掉它毫无差别——目标端提前回 413、客户端还
	// 在流 512KB 请求体的场景，两种写法都在 1ms 内把响应送到了（判据是
	// TestH2CRequestBodyIsFullyForwarded 与 TestH2CEarlyResponseWithBodyInFlight）。
	// net/http.Server 那条"响应先写就必须先读完请求体"的规则属于 stdlib server，
	// 真要改用它是另一轮带判据的改动，不在这里先埋代码。
	//
	// 也不能 defer reqWriter.Close()：请求写完就发 EOF 会让 relay
	// （「任一方向结束就拆掉整个会话」）把还在路上的响应砍断 —— 真实栈实测
	// 64KB chunked 响应变成 unexpected EOF + 502。请求方向的 EOF 统一由
	// OtherConn.Close() 发出，也就是本条会话真正结束的时候。
	go func() {
		if err := r.Write(reqWriter); err != nil {
			log.Error(common.NewError("http2 failed to write request downstream").Base(err))
		}
	}()

	resp, err := http.ReadResponse(bufio.NewReader(io.NopCloser(respReader)), r)
	if err != nil {
		log.Error(common.NewError("http2 failed to read response").Base(err))
		reqWriter.Close() // 唤醒可能还堵在推请求体上的那个 goroutine
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if hopByHopHeader(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	copyBuf := getH2CopyBuf()
	defer putH2CopyBuf(copyBuf)
	if _, err := io.CopyBuffer(w, resp.Body, copyBuf); err != nil {
		log.Error(common.NewError("http2 failed to write the response back").Base(err))
	}
}

// hopByHopHeader 列的是 h2 不允许出现在连接里的逐跳头：目标端按 h1 回的东西
// 常常带这些，照抄会让 http2 框架直接报错断流。
func hopByHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-connection", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "host":
		return true
	}
	return false
}
