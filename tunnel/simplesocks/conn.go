package simplesocks

import (
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// Conn is a simplesocks connection
type Conn struct {
	tunnel.Conn
	metadata      *tunnel.Metadata
	isOutbound    bool
	headerWritten bool
}

func (c *Conn) Metadata() *tunnel.Metadata {
	return c.metadata
}

func (c *Conn) Write(payload []byte) (int, error) {
	if c.isOutbound && !c.headerWritten {
		// 头 = CMD + 地址块，后面直接跟 payload。按 payload 长度 +64 一次性预留：
		// 64 够放 ATYP + 长度字节 + IPv4/IPv6 + 端口 + 一个常见长度的域名，更长的域名
		// 让 append 自己扩一次容（只影响性能，不改字节）。旧写法固定预留 4096，而这条
		// 路径是**每条 mux 流**走一次——一个浏览器会话几百条流就是几百个 4KB 的抖动。
		out := make([]byte, 0, len(payload)+64)
		out, err := c.metadata.AppendTo(out)
		if err != nil {
			// 旧实现把 WriteTo 的错误直接丢掉了：地址编不进线上格式时照样把缓冲区里
			// 那点残缺字节发出去，对端按状态机解就流错位。这里一个字都还没写，判死
			// 这一条流比发出坏字节干净。
			return 0, common.NewError("failed to serialize simplesocks header").Base(err)
		}
		out = append(out, payload...)
		if _, err := c.Conn.Write(out); err != nil {
			return 0, common.NewError("failed to write simplesocks header").Base(err)
		}
		c.headerWritten = true
		return len(payload), nil
	}
	return c.Conn.Write(payload)
}

// PacketConn is a simplesocks packet connection
// The header syntax is the same as trojan's
type PacketConn struct {
	trojan.PacketConn
}
