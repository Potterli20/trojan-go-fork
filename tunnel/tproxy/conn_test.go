//go:build linux

package tproxy

import (
	"bytes"
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// WriteWithMetadata 必须把 payload **复制**进自己新分配的切片再入队，两个理由各对应
// 一条断言：
//  1. 不复制就等于把调用方的 buffer 交给另一个 goroutine——proxy 的转发环在
//     WriteWithMetadata 返回后立刻把这块 buffer 还回池子并复用（proxy.go 的
//     packetPool.Put），队列里的包随后会被覆写；
//  2. 复制的目标必须是*有内容*的副本。此前这里只 make 不 copy，导致经 tproxy 的
//     UDP 回包 payload 恒为全零——数据被静默损坏。同结构的 socks/conn.go:60-61
//     是 make + copy，那才是正确写法。
func TestPacketConnWriteCopiesPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &PacketConn{
		input:  make(chan *packetInfo),
		output: make(chan *packetInfo, 1),
		ctx:    ctx,
	}

	payload := []byte("trojan-go udp payload")
	metadata := &tunnel.Metadata{
		Command: tunnel.Command(0x03), // trojan/socks 线格式里的 UDP
		Address: &tunnel.Address{AddressType: tunnel.IPv4, Port: 53},
	}

	n, err := c.WriteWithMetadata(payload, metadata)
	if err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("返回写入长度 %d，期望 %d", n, len(payload))
	}

	info := <-c.output
	if !bytes.Equal(info.payload, payload) {
		t.Fatalf("入队的 payload 是 %q，期望 %q——内容没有被复制过去", info.payload, payload)
	}

	// 调用方随后改写自己的 buffer，不得影响已经入队的那份
	overwrite := make([]byte, len(payload))
	for i := range overwrite {
		overwrite[i] = 'X'
	}
	copy(payload, overwrite)
	if bytes.Equal(info.payload, payload) {
		t.Fatal("入队的仍是调用方的 buffer 本体（零拷贝），池复用后这个包会被覆写")
	}
}
