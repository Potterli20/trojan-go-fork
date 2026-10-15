package trojan

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// recordConn 记录每一次落到"线上"的 Write，用于判定序列化失败时到底有没有发出字节。
type recordConn struct {
	mu     sync.Mutex
	writes [][]byte
}

func (c *recordConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	c.writes = append(c.writes, cp)
	return len(p), nil
}

func (c *recordConn) written() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	copy(out, c.writes)
	return out
}

func (c *recordConn) Metadata() *tunnel.Metadata       { return &tunnel.Metadata{} }
func (c *recordConn) Read(b []byte) (int, error)       { return 0, io.EOF }
func (c *recordConn) Close() error                     { return nil }
func (c *recordConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *recordConn) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (c *recordConn) SetDeadline(time.Time) error      { return nil }
func (c *recordConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordConn) SetWriteDeadline(time.Time) error { return nil }

// TestWriteWithMetadataSerializeFailureWritesNothing 钉住 UDP datagram 头的序列化错误。
//
// 旧写法是 `metadata.Address.WriteTo(w)` 并丢弃返回值（//gosec:disable）。WriteTo 在
// 失败时确实不会往 w 里留半截字节，但**调用方继续往下走**：地址块整个没写进去，
// length + CRLF + payload 照样发出，于是流里出现一个"没有地址的 datagram"，对端按
// trojan 的 datagram 状态机解就永久错位——整条 UDP 中继从此废掉，而且是静默的。
// 判据：出错必须一个字都不发，并且返回错误。
func TestWriteWithMetadataSerializeFailureWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		address *tunnel.Address
	}{
		{
			name:    "非法 ATYP",
			address: &tunnel.Address{AddressType: tunnel.AddressType(99), DomainName: "example.com", Port: 443},
		},
		{
			name: "域名超过单字节长度上限",
			address: &tunnel.Address{
				AddressType: tunnel.DomainName,
				DomainName:  strings.Repeat("a", 256),
				Port:        443,
			},
		},
		{
			name:    "端口越界",
			address: &tunnel.Address{AddressType: tunnel.IPv4, IP: net.IPv4(1, 1, 1, 1).To4(), Port: 70000},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordConn{}
			pc := &PacketConn{Conn: rec}
			payload := []byte("hello over udp")

			_, err := pc.WriteWithMetadata(payload, &tunnel.Metadata{Address: tc.address})
			if err == nil {
				t.Fatal("地址序列化失败却返回了成功")
			}
			if got := rec.written(); len(got) != 0 {
				t.Fatalf("序列化失败仍往流里写出了 %d 段字节（%q）：对端状态机会永久错位",
					len(got), got[0])
			}
		})
	}
}

// TestWriteWithMetadataNilAddressDoesNotPanic 是防御性判据：Address 为 nil 时
// 旧写法会在 `metadata.Address.WriteTo(w)` 里直接 nil 解引用崩掉中继协程。
// 全仓确实存在产出空地址 metadata 的点（tunnel/quic/client.go:283,290 的
// `&tunnel.Metadata{}`）；trojan 当前的栈序能不能收到它取决于配置组合，
// 这里不声称已实测可达，只要求"即便收到也只报错、不崩进程"。
func TestWriteWithMetadataNilAddressDoesNotPanic(t *testing.T) {
	rec := &recordConn{}
	pc := &PacketConn{Conn: rec}

	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("nil Address 触发 panic，中继协程会当场崩掉：%v", r)
			}
			close(done)
		}()
		if _, err := pc.WriteWithMetadata([]byte("x"), &tunnel.Metadata{}); err == nil {
			t.Error("nil Address 应当返回错误")
		}
	}()
	<-done

	if got := rec.written(); len(got) != 0 {
		t.Fatalf("nil Address 仍写出字节 %q", got[0])
	}
}

// TestMaxSizePacketFitsPooledCapacity 钉住池的归还闸门为什么此前形同失效。
//
// 旧写法 New 只预留 MaxPacketSize，而一帧最坏是「ATYP(1)+域名长度字节(1)+255 域名+端口(2)」
// + 长度(2) + CRLF(2) + 满负载(8192)，必然超出预留 → append 扩容到 16KB → 归还闸门
// `cap(frame) <= packetBufCap` 判负 → 对象永不回池，每个满尺寸包都重新分配。
//
// 这里刻意**不**用"写完再 Get、看有没有新建对象"来判：sync.Pool 的本地队列会在 P 之间
// 被窃取，`-race` 下实测有约 30% 的假失效，任何身份/计数判据都稳不住。撑没撑爆预留
// 是纯确定性的容量事实，直接钉它就够。
func TestMaxSizePacketFitsPooledCapacity(t *testing.T) {
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	reserved := cap(*bufp)

	// 最坏合法帧：255 字节域名（AppendTo 允许的上限）+ 满尺寸负载
	worst := &tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  strings.Repeat("a", 255),
		Port:        65535,
	}
	frame, err := worst.AppendTo((*bufp)[:0])
	if err != nil {
		t.Fatalf("最坏合法地址本应能序列化: %v", err)
	}
	frame = append(frame, 0x1f, 0xf8) // 长度字段 = MaxPacketSize
	frame = append(frame, '\r', '\n')
	frame = append(frame, make([]byte, MaxPacketSize)...)

	if cap(frame) != reserved {
		t.Fatalf("最坏合法帧把预留缓冲从 %d 撑到 %d：归还闸门按 cap 判定，被撑大的对象永不回池",
			reserved, cap(frame))
	}
	if reserved < packetBufCap {
		t.Fatalf("池的预留 %d 小于声明的 packetBufCap %d", reserved, packetBufCap)
	}
}
