package freedom

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

func socksTestAddress() *tunnel.Address {
	return &tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  "dns.example.com",
		Port:        53,
		NetworkType: "udp",
	}
}

// TestSocksPacketConnReadKeepsPayloadIntact SocksPacketConn 的两个方向都从池里
// 借每包缓冲（此前每包 make 一次 8KB）。这里驱动多个内容互不相同的包，验证
// 归还时机没有把上一包的内容留给下一包：帧内容 = RSV(3) + address + payload。
func TestSocksPacketConnReadKeepsPayloadIntact(t *testing.T) {
	a, err := net.ListenPacket("udp", "127.0.0.1:0")
	common.Must(err)
	defer a.Close() //gosec:disable -- 错误忽略：测试清理
	b, err := net.ListenPacket("udp", "127.0.0.1:0")
	common.Must(err)
	defer b.Close() //gosec:disable -- 错误忽略：测试清理

	aAddr := a.LocalAddr().(*net.UDPAddr)
	// socksClient 保持 nil：本测试不触发 Close 的 socksClient 分支
	c := &SocksPacketConn{PacketConn: a, socksAddr: aAddr}

	addr := socksTestAddress()
	payloads := [][]byte{
		make([]byte, 64),
		make([]byte, 1200),
		make([]byte, 512),
	}
	for i, p := range payloads {
		for j := range p {
			p[j] = byte(i*31 + j)
		}
	}

	buf := make([]byte, MaxPacketSize)
	for i, payload := range payloads {
		frame := bytes.NewBuffer(nil)
		frame.Write([]byte{0, 0, 0})
		_, err := addr.WriteTo(frame)
		common.Must(err)
		frame.Write(payload)

		_, err = b.WriteTo(frame.Bytes(), aAddr)
		common.Must(err)

		n, meta, err := c.ReadWithMetadata(buf)
		if err != nil {
			t.Fatalf("第 %d 个包读取失败: %v", i, err)
		}
		if n != len(payload) {
			t.Fatalf("第 %d 个包长度 %d，期望 %d", i, n, len(payload))
		}
		if !bytes.Equal(buf[:n], payload) {
			t.Fatalf("第 %d 个包内容不符：缓冲池把上一包留给了下一包", i)
		}
		if meta == nil || meta.Address.DomainName != "dns.example.com" || meta.Address.Port != 53 {
			t.Fatalf("第 %d 个包 metadata 异常: %v", i, meta)
		}
		for j := range buf {
			buf[j] = 0xFF
		}
	}
}

// TestSocksPacketConnWriteFraming 覆盖写出方向的复用缓冲：写头必须是 3 字节 RSV
// 加地址再加完整 payload。最后一个用例的 payload 就是 MaxPacketSize，整帧超出
// scratch 容量，用来确认 bytes.Buffer 的再分配路径没有被池化改动破坏。
func TestSocksPacketConnWriteFraming(t *testing.T) {
	a, err := net.ListenPacket("udp", "127.0.0.1:0")
	common.Must(err)
	defer a.Close() //gosec:disable -- 错误忽略：测试清理
	b, err := net.ListenPacket("udp", "127.0.0.1:0")
	common.Must(err)
	defer b.Close() //gosec:disable -- 错误忽略：测试清理

	bAddr := b.LocalAddr().(*net.UDPAddr)
	c := &SocksPacketConn{PacketConn: a, socksAddr: bAddr}
	addr := socksTestAddress()

	for _, size := range []int{64, 1200, MaxPacketSize} {
		payload := make([]byte, size)
		for j := range payload {
			payload[j] = byte(size + j)
		}
		n, err := c.WriteWithMetadata(payload, &tunnel.Metadata{Address: addr})
		if err != nil {
			t.Fatalf("size %d 写出失败: %v", size, err)
		}
		if n != size {
			t.Fatalf("size %d 返回 %d", size, n)
		}

		recv := make([]byte, 2*MaxPacketSize)
		if err := b.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		got, from, err := b.ReadFrom(recv)
		if err != nil {
			t.Fatalf("size %d 读回失败: %v", size, err)
		}
		if from.String() != a.LocalAddr().String() {
			t.Fatalf("size %d 来源地址 %v，期望 %v", size, from, a.LocalAddr())
		}
		if got < 3+size {
			t.Fatalf("size %d 帧长度 %d，不足 RSV+payload", size, got)
		}
		if !bytes.Equal(recv[:3], []byte{0, 0, 0}) {
			t.Fatalf("size %d 的 RSV 头不对: %v", size, recv[:3])
		}
		r := bytes.NewBuffer(recv[3:got])
		parsed := new(tunnel.Address)
		if _, err := parsed.ReadFrom(r); err != nil {
			t.Fatalf("size %d 解析地址失败: %v", size, err)
		}
		if parsed.DomainName != "dns.example.com" || parsed.Port != 53 {
			t.Fatalf("size %d 地址不符: %v", size, parsed)
		}
		body, err := io.ReadAll(r)
		common.Must(err)
		if !bytes.Equal(body, payload) {
			t.Fatalf("size %d 的 payload 被截断或串包：got %d, want %d", size, len(body), len(payload))
		}
	}
}
