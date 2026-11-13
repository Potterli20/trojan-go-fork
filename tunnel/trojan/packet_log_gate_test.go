package trojan

import (
	"bytes"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingRemoteConn struct {
	readBuf     *bytes.Reader
	remoteCalls atomic.Int32
}

func (c *countingRemoteConn) Read(p []byte) (int, error)  { return c.readBuf.Read(p) }
func (c *countingRemoteConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *countingRemoteConn) Close() error                { return nil }
func (c *countingRemoteConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *countingRemoteConn) RemoteAddr() net.Addr {
	c.remoteCalls.Add(1)
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
}
func (c *countingRemoteConn) SetDeadline(time.Time) error      { return nil }
func (c *countingRemoteConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countingRemoteConn) SetWriteDeadline(time.Time) error { return nil }
func (c *countingRemoteConn) Metadata() *tunnel.Metadata       { return nil }

func udpFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	addr := &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53}
	buf, err := addr.AppendTo(nil)
	if err != nil {
		t.Fatalf("serialize addr: %v", err)
	}
	buf = append(buf, byte(len(payload)>>8), byte(len(payload)), '\r', '\n')
	return append(buf, payload...)
}

// 每包日志在级别关闭时也必须先判级别：Debug 的实参 c.RemoteAddr() 是每包一次
// 的系统调用/格式化，UDP 包速率下这是纯白付的开销。
func TestPacketLogGate(t *testing.T) {
	old := log.GetLogLevel()
	log.SetLogLevel(log.OffLevel)
	t.Cleanup(func() { log.SetLogLevel(old) })

	dst := &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53}
	writeConn := &countingRemoteConn{readBuf: bytes.NewReader(nil)}
	pc := &PacketConn{Conn: writeConn}
	if _, err := pc.WriteWithMetadata([]byte("hello"), &tunnel.Metadata{Address: dst}); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}
	if n := writeConn.remoteCalls.Load(); n != 0 {
		t.Fatalf("日志关闭时 WriteWithMetadata 仍调了 %d 次 RemoteAddr：实参先于门禁求值", n)
	}

	readConn := &countingRemoteConn{readBuf: bytes.NewReader(udpFrame(t, []byte("hello")))}
	pc2 := &PacketConn{Conn: readConn}
	if _, _, err := pc2.ReadWithMetadata(make([]byte, 64)); err != nil {
		t.Fatalf("ReadWithMetadata: %v", err)
	}
	if n := readConn.remoteCalls.Load(); n != 0 {
		t.Fatalf("日志关闭时 ReadWithMetadata 仍调了 %d 次 RemoteAddr：实参先于门禁求值", n)
	}
}

// 结构门禁兜住 Record 那条路（Record 里 RemoteAddr 同时是 recorder.Add 的实参，
// 行为断言分不出日志那次调用）：packet.go 里每个 log.Debug 前必须紧邻 ShouldLog 门禁。
func TestPacketLogCallsAreGated(t *testing.T) {
	src, err := os.ReadFile("packet.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	debugs := 0
	for i, line := range lines {
		if !strings.Contains(line, "log.Debug(") {
			continue
		}
		debugs++
		prev := i - 1
		for prev >= 0 && strings.TrimSpace(lines[prev]) == "" {
			prev--
		}
		if prev < 0 || !strings.Contains(lines[prev], "log.ShouldLog(log.DebugLevel)") {
			t.Fatalf("packet.go:%d 的 log.Debug 没有紧邻 ShouldLog 门禁", i+1)
		}
	}
	if debugs < 3 {
		t.Fatalf("只找到 %d 处 log.Debug（期望 3 处），锚点可能漂移", debugs)
	}
}