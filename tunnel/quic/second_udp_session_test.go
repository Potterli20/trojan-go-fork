package quic

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 同一条 QUIC 连接上连续建立两条 UDP 会话：第一条正常关闭后，第二条必须仍能往返。
//
// 缺陷形态：服务端数据报泵把会话级 udpSession.done 当成整条泵的退出条件。
// 第一条会话关闭 → close(done) → 泵 return，此后这条 QUIC 连接上再无人调用
// ReceiveDatagram。客户端 DialPacket 复用缓存的同一 *quic.Conn（不会新建泵），
// 第二条会话发出的数据报全部堆在接收队列无人消费 → 永久挂死。
//
// 这是 QUIC+UDP 的常规用法（同一连接多条 UDP 会话），不需要异常报文。
func TestQuicSecondUDPSessionOnSameConnection(t *testing.T) {
	pair := newQuicPair(t)
	addr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "dns.example.com", Port: 53}

	// 服务端：连续接受两条 UDP 会话，各自回显一个包。
	serverDone := make(chan error, 2)
	go func() {
		for i := range 2 {
			pc, err := pair.server.AcceptPacket(nil)
			if err != nil {
				serverDone <- fmt.Errorf("第 %d 条 AcceptPacket: %w", i+1, err)
				return
			}
			buf := make([]byte, testPacketSize)
			n, _, err := pc.ReadWithMetadata(buf)
			if err != nil {
				pc.Close()
				serverDone <- fmt.Errorf("第 %d 条 server read: %w", i+1, err)
				return
			}
			payload := append([]byte(nil), buf[:n]...)
			if _, err := pc.WriteWithMetadata(payload, &tunnel.Metadata{Address: addr}); err != nil {
				pc.Close()
				serverDone <- fmt.Errorf("第 %d 条 server write: %w", i+1, err)
				return
			}
			pc.Close() // 关掉这一条会话——正是这一步曾让整条数据报泵退出
			serverDone <- nil
		}
	}()

	// 客户端：在同一 pair.client 上依次 DialPacket 两次，各发一个包、读回显。
	clientDone := make(chan error, 2)
	go func() {
		for i := range 2 {
			pc, err := pair.client.DialPacket(nil)
			if err != nil {
				clientDone <- fmt.Errorf("第 %d 条 DialPacket: %w", i+1, err)
				return
			}
			payload := []byte(fmt.Sprintf("udp-session-%d", i+1))
			if _, err := pc.WriteWithMetadata(payload, &tunnel.Metadata{Address: addr}); err != nil {
				pc.Close()
				clientDone <- fmt.Errorf("第 %d 条 client write: %w", i+1, err)
				return
			}
			buf := make([]byte, testPacketSize)
			deadline := time.Now().Add(10 * time.Second)
			for {
				n, _, err := pc.ReadWithMetadata(buf)
				if err != nil {
					pc.Close()
					clientDone <- fmt.Errorf("第 %d 条 client read: %w", i+1, err)
					return
				}
				if n == 0 {
					if time.Now().After(deadline) {
						pc.Close()
						clientDone <- fmt.Errorf("第 %d 条一直读到空数据报", i+1)
						return
					}
					continue
				}
				if !bytes.Equal(buf[:n], payload) {
					pc.Close()
					clientDone <- fmt.Errorf("第 %d 条回显 %q，期望 %q", i+1, buf[:n], payload)
					return
				}
				pc.Close() // 关掉客户端这条会话，下一条仍用同一 client/连接
				clientDone <- nil
				break
			}
		}
	}()

	for i := range 2 {
		select {
		case err := <-clientDone:
			if err != nil {
				t.Fatalf("客户端第 %d 条 UDP 会话: %v", i+1, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("客户端第 %d 条 UDP 会话超时（同连接上第一条关闭后第二条无数据报泵）", i+1)
		}
		select {
		case err := <-serverDone:
			if err != nil {
				t.Fatalf("服务端第 %d 条 UDP 会话: %v", i+1, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("服务端第 %d 条 UDP 会话超时", i+1)
		}
	}
}
