package freedom

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// scribbleConn 是一个可控的 net.PacketConn：ReadFrom 把帧写进**调用方给的**切片，
// 并把那块切片本身留在 lastBuf 上——测试于是拿到 pooled 缓冲的引用，可以在
// ReadWithMetadata 返回、缓冲已经归还进池之后，再把它涂脏。
type scribbleConn struct {
	frame   []byte
	lastBuf []byte
}

func (c *scribbleConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n := copy(p, c.frame)
	c.lastBuf = p
	return n, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, nil
}

func (c *scribbleConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (c *scribbleConn) LocalAddr() net.Addr                       { return &net.UDPAddr{} }
func (c *scribbleConn) RemoteAddr() net.Addr                      { return &net.UDPAddr{} }
func (c *scribbleConn) SetDeadline(time.Time) error               { return nil }
func (c *scribbleConn) SetReadDeadline(time.Time) error           { return nil }
func (c *scribbleConn) SetWriteDeadline(time.Time) error          { return nil }
func (c *scribbleConn) Close() error                              { return nil }

// TestParsedAddressDoesNotAliasPooledBuffer 把 ReadWithMetadata 依赖的那条隐式不变量
// 变成判据。
//
// `r := bytes.NewBuffer(buf[3:n])` 交出去的是**池化缓冲的别名**，而 `defer
// putSocksPacketBuf(buf)` 在同一个函数作用域里就把它还回池子。今天不出事，靠的是
// Address.ReadFrom 只把字节读进本地数组、域名走 string() 复制——任何一方改成
// "直接持有底层切片的一小段"，就会变成跨包串数据（下一个使用者覆写它时，上一条
// 会话的地址/负载还在被引用），而且是随机复现的那种。
//
// 判据：解析完成后把那块缓冲整个涂脏，已返回的地址与负载必须一个字节都不变。
func TestParsedAddressDoesNotAliasPooledBuffer(t *testing.T) {
	addr := &tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "dns.example.com",
		Port:        53,
	}
	header := []byte{0, 0, 0}
	frame := bytes.NewBuffer(header)
	if _, err := addr.WriteTo(frame); err != nil {
		t.Fatalf("构造测试帧: %v", err)
	}
	payload := make([]byte, 200)
	for i := range payload {
		payload[i] = byte(i + 7)
	}
	frame.Write(payload)

	conn := &scribbleConn{frame: frame.Bytes()}
	c := &SocksPacketConn{PacketConn: conn, socksAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}

	out := make([]byte, MaxPacketSize)
	n, meta, err := c.ReadWithMetadata(out)
	if err != nil {
		t.Fatalf("ReadWithMetadata: %v", err)
	}
	if meta == nil || meta.Address == nil {
		t.Fatal("没有返回地址 metadata")
	}
	if conn.lastBuf == nil {
		t.Fatal("测试前置条件不成立：没有拿到调用方缓冲的引用")
	}

	// 缓冲此时已归还进池：涂脏它模拟"下一个使用者写入"
	gotDomain := meta.Address.DomainName
	gotIP := append(net.IP(nil), meta.Address.IP...)
	for i := range conn.lastBuf {
		conn.lastBuf[i] = 0xFF
	}

	if meta.Address.DomainName != gotDomain || gotDomain != "dns.example.com" {
		t.Fatalf("解析出的域名被池化缓冲的后续写入影响：got=%q want=%q", meta.Address.DomainName, gotDomain)
	}
	if !bytes.Equal(meta.Address.IP, gotIP) {
		t.Fatalf("解析出的 IP 被池化缓冲的后续写入影响：% x -> % x", gotIP, meta.Address.IP)
	}
	if n != len(payload) || !bytes.Equal(out[:n], payload) {
		t.Fatalf("负载被池化缓冲的后续写入影响：n=%d 前 %d 字节=% x", n, min(len(out), 16), out[:16])
	}
}
