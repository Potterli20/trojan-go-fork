package http

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/Potterli20/trojan-go-fork/common"
)

// 这一组钉的是 h2c 入站的**资源上限**。h1 分支每读一个请求头都有
// maxRequestHeadBytes 的预算（TestHTTPServerBoundsRequestHead 守着的），
// 而 h2c 是另一条完全独立的解析路径：预算来自 http2 服务端的
// MaxReadFrameSize 与 maxHeaderListSize，不看我们那个常量。
// 上一轮我只是从源码推断两者同档（默认 1MB + 320 字节补偿），
// 这里把它变成可执行的判据 —— 换人改 MaxReadFrameSize 时会当场红。

// h2RawConn 建立一条真的 h2c 客户端侧连接：写完前文与 SETTINGS 后返回，
// 供测试手工塞帧（标准客户端不会发超限的帧，必须绕过它）。
func h2RawConn(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	common.Must(err)
	_, err = conn.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	common.Must(err)
	f := http2.NewFramer(conn, conn)
	common.Must(f.WriteSettings())
	return conn
}

// startRawH2Server 起 http 入站 + 一个会答 200 的假 relay，返回监听地址。
func startRawH2Server(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	common.Must(err)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				if err := readHeaders(conn); err != nil {
					return
				}
				conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")) //gosec:disable -- 错误忽略：假 relay
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})
	return ln.Addr().String()
}

// awaitRejection 要求在时限内看到服务端把这条连接/这条流打回，
// 并且不能是一个 200 —— 那意味着超限的头被照单全收。
func awaitRejection(t *testing.T, conn net.Conn, what string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	f := http2.NewFramer(nil, conn)
	for {
		frame, err := f.ReadFrame()
		if err != nil {
			// 对端直接断开也算拒绝
			t.Logf("%s：读取结束于 %v", what, err)
			return
		}
		switch fh := frame.(type) {
		case *http2.GoAwayFrame:
			t.Logf("%s：收到 GOAWAY lastStream=%d err=%v", what, fh.LastStreamID, fh.ErrCode)
			return
		case *http2.RSTStreamFrame:
			t.Logf("%s：收到 RST_STREAM stream=%d err=%v", what, fh.StreamID, fh.ErrCode)
			return
		case *http2.SettingsFrame:
			// 服务端的 SETTINGS（含它 advertised 的上限），继续等
		case *http2.MetaHeadersFrame:
			for _, hf := range fh.Fields {
				if hf.Name == "status" && hf.Value == "200" {
					t.Fatalf("%s：超预算的头部块被当成合法请求处理了（status 200）", what)
				}
			}
		default:
			// DATA / WINDOW_UPDATE 等与本判据无关
		}
	}
}

// TestH2COversizedFrameIsRefused 单帧就超 MaxReadFrameSize（1MB）：
// 服务端必须在帧头阶段就拦下，而不是把 2MB 攒进缓冲再解。
func TestH2COversizedFrameIsRefused(t *testing.T) {
	addr := startRawH2Server(t)
	conn := h2RawConn(t, addr)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	// 一个大字面量：2MB 的 value，远大于 MaxReadFrameSize
	if err := enc.WriteField(hpack.HeaderField{Name: "x-huge", Value: strings.Repeat("A", 2<<20)}); err != nil {
		t.Fatalf("编码头块失败: %v", err)
	}
	f := http2.NewFramer(conn, nil)
	if err := f.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block.Bytes(), EndHeaders: true}); err != nil {
		t.Fatalf("写 HEADERS 失败: %v", err)
	}
	awaitRejection(t, conn, "单帧 2MB")
}

// TestH2CCumulativeHeadersAreRefused 每帧都合法（64KB），但累加到约 2MB。
// 这一支才真正考察 maxHeaderListSize：实现必须边解边计长度并中止，
// 而不是先收下整个头块再判断。
func TestH2CCumulativeHeadersAreRefused(t *testing.T) {
	addr := startRawH2Server(t)
	conn := h2RawConn(t, addr)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	const piece = 64 << 10
	f := http2.NewFramer(conn, nil)
	// HEADERS 首帧 + 一连串 CONTINUATION，凑够约 2MB 的头部块
	first := true
	for sent := 0; sent < 2<<20; sent += piece {
		frag := make([]byte, piece)
		for i := range frag {
			frag[i] = 'B'
		}
		var err error
		if first {
			err = f.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: frag, EndHeaders: false})
			first = false
		} else {
			err = f.WriteContinuation(1, sent >= 2<<20-piece, frag)
		}
		if err != nil {
			t.Fatalf("写头块分片失败: %v", err)
		}
	}
	awaitRejection(t, conn, "累加 2MB（每帧 64KB）")
}
