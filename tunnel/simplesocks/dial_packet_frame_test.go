package simplesocks

import (
	"bytes"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// frameRecorder 只关心 DialPacket 落到线上的那一段握手帧。
type frameRecorder struct {
	net.Conn
	buf bytes.Buffer
}

func (c *frameRecorder) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *frameRecorder) Metadata() *tunnel.Metadata  { return nil }

type recordingDialer struct{ conn tunnel.Conn }

func (d *recordingDialer) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return d.conn, nil
}

func (d *recordingDialer) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported")
}

func (d *recordingDialer) Close() error { return nil }

// TestDialPacketAssociateFrameIsUnchanged 钉住 DialPacket 换成 AppendTo 之后的字节。
//
// UDP associate 帧 = Command(1) + ATYP(1) + 域名长度(1) + "UDP_CONN"(8) + 端口(2)
// = 13 字节。判据用手写字面量：这里要保证的是"改造不改变线上格式"，
// 用实现再算一遍等于什么都没测。
func TestDialPacketAssociateFrameIsUnchanged(t *testing.T) {
	pipeA, pipeB := net.Pipe()
	t.Cleanup(func() {
		pipeA.Close() //gosec:disable -- 错误忽略：测试清理
		pipeB.Close() //gosec:disable -- 错误忽略：测试清理
	})
	rec := &frameRecorder{Conn: pipeA}
	c := &Client{underlay: &recordingDialer{conn: rec}}

	pc, err := c.DialPacket(nil)
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	if pc == nil {
		t.Fatal("DialPacket 返回了 nil")
	}

	want := []byte{
		byte(Associate), byte(tunnel.DomainName), 8,
		'U', 'D', 'P', '_', 'C', 'O', 'N', 'N',
		0x00, 0x00,
	}
	got := rec.buf.Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("UDP associate 帧字节变了\n got % x\nwant % x", got, want)
	}
	if len(got) != 13 {
		t.Fatalf("帧长 %d，期望 13", len(got))
	}
}
