package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 用一个 net.Pipe 充当 underlay 递给 Server，不需要真的监听端口。
type pipeServer struct {
	conn tunnel.Conn
}

func (s *pipeServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	if s.conn == nil {
		<-time.After(time.Second)
		return nil, fmt.Errorf("no more conns")
	}
	c := s.conn
	s.conn = nil
	return c, nil
}

func (s *pipeServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, fmt.Errorf("no packet")
}

func (s *pipeServer) Close() error { return nil }

type pipeConn struct {
	net.Conn
}

func (c *pipeConn) Metadata() *tunnel.Metadata {
	return &tunnel.Metadata{Command: tunnel.Command(0x01)}
}

// 用"很少的头部行、但每行很大"的形态：如果按几千条小行去撑体积，会先撞上 Go 1.27
// 新增的头部条目数上限（DefaultMaxHeaderValueCount=500），那样测试测的就不是字节预算
// 了，把字节预算删掉也照样"通过"——这是我给这条测试第一次做变异时踩到的假绿。
func writeConnectHead(client net.Conn, headerBytes int) {
	fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n")
	perLine := 512 * 1024
	lines := headerBytes/perLine + 1
	big := strings.Repeat("x", perLine)
	for i := range lines {
		fmt.Fprintf(client, "X-Big%d: %s\r\n", i, big)
	}
	fmt.Fprint(client, "\r\n")
}

// TestHTTPServerBoundsRequestHead 盯的是：http.ReadRequest 自身没有头部上限
// （stdlib 里有上限的是内部 readRequestLimit，只给 Server 用），未认证的对端可以只用
// 请求头把单条连接的内存推到任意大。transport/tls/websocket 各层嗅探时都限了幅，
// 本文件这个真实代理入口此前漏了。
//
// 变异方向也验证了判据不空转：去掉 boundedReader.Limit 后，超大请求会被正常解析并回
// "200 Connection established"，本测试转红；而合法的小请求在两种情况下都能拿到 200，
// 所以第二条测试不会被误伤。
func TestHTTPServerBoundsRequestHead(t *testing.T) {
	client, serverSide := net.Pipe()
	srv, err := NewServer(context.Background(), &pipeServer{conn: &pipeConn{Conn: serverSide}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close() //gosec:disable -- 错误忽略：测试清理

	// 远超 maxRequestHeadBytes（1MB）的请求头
	go writeConnectHead(client, 4*1024*1024)

	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err == nil && strings.Contains(string(buf[:n]), "200") {
		t.Fatalf("4MB 的请求头被完整解析并回了 %q——头部预算没有生效", string(buf[:n]))
	}
	// 期望：解析在预算耗尽处失败，连接被关闭
	if err != nil && err != io.EOF && !strings.Contains(err.Error(), "use of closed") {
		t.Fatalf("期望连接被服务端关闭，实际错误：%v", err)
	}
}

// TestHTTPServerAcceptsNormalRequestHead 是同一条路径的阳性对照：低于上限的请求头
// 必须照常工作，否则上一条测试就成了"永远拒绝"的空转判据。
func TestHTTPServerAcceptsNormalRequestHead(t *testing.T) {
	client, serverSide := net.Pipe()
	srv, err := NewServer(context.Background(), &pipeServer{conn: &pipeConn{Conn: serverSide}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close() //gosec:disable -- 错误忽略：测试清理

	go writeConnectHead(client, 64*1024) // 64KB，远小于 1MB

	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("读取响应失败：%v", err)
	}
	if !strings.Contains(string(buf[:n]), "200 Connection established") {
		t.Fatalf("合法请求被拒绝，响应是 %q", string(buf[:n]))
	}
}

// TestEveryReadRequestSiteIsBounded 是结构性守卫：本文件里每一个
// http.ReadRequest(reqBufReader) 调用点都必须成对出现 Limit / Unlimited。
// 上面那条行为测试只覆盖首个请求；keep-alive 的第二个调用点要在测试里走通真实的
// 上游转发，代价过高，所以这里退而锁结构——并带上限与下限双重自证，避免"正则
// 从没匹配过也照样绿"的永真判据。
func TestEveryReadRequestSiteIsBounded(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go 失败: %v", err)
	}
	text := string(src)
	callSites := strings.Count(text, "http.ReadRequest(reqBufReader)")
	limits := strings.Count(text, "boundedReader.Limit(maxRequestHeadBytes)")
	unlimits := strings.Count(text, "boundedReader.Unlimited()")
	if callSites != 2 {
		t.Fatalf("ReadRequest 调用点数变成 %d（期望 2）：本守卫的假设已失效，请重新核对每处都有预算", callSites)
	}
	if limits != callSites || unlimits != callSites {
		t.Fatalf("预算与调用点不配对：Limit=%d Unlimited=%d 调用点=%d", limits, unlimits, callSites)
	}
}
