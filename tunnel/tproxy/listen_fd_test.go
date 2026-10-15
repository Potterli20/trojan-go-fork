//go:build linux

package tproxy

import (
	"net"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
)

// setsockoptFailure 判定错误确实出自设置 socket 选项这一步，而不是 net.Listen 本身。
// 只有前者才意味着"已经 bind 住端口的 socket 被丢弃"，也才是本文件要钉的东西。
func setsockoptFailure(t *testing.T, err error) bool {
	t.Helper()
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "set socket option") || strings.Contains(err.Error(), "get file descriptor") {
		return true
	}
	t.Skipf("构造不出「已 bind 但选项设置失败」的路径，实际错误: %v", err)
	return false
}

// TestListenTCPReleasesPortWhenSetsockoptFails 钉住 ListenTCP 的失败回收。
//
// 旧写法在 setsockopt 出错时直接返回错误，而 `listener.File()` 拿到的是 dup fd，
// defer 关掉的只是那一层副本——真正 bind 住端口的是 listener 自己的 socket。
// 没有 CAP_NET_ADMIN 时（CI 与本机都是 EPERM）这条是常态路径，每次失败建栈都要
// 把一个已占用的端口连 fd 一起留在进程里；在库模式反复建栈（api、测试）就逐次累计。
func TestListenTCPReleasesPortWhenSetsockoptFails(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	laddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}

	_, err := ListenTCP("tcp", laddr)
	if !setsockoptFailure(t, err) {
		return
	}

	retry, retryErr := net.ListenTCP("tcp", laddr)
	if retryErr != nil {
		t.Fatalf("ListenTCP 失败后端口仍被占用，socket 泄露：%v", retryErr)
	}
	retry.Close() //gosec:disable -- 错误忽略：测试清理
}

// TestListenUDPReleasesPortWhenSetsockoptFails 是同一条判据的 UDP 侧。
// ListenUDP 有三个 setsockopt 出口，每一个都得把已 bind 的 *net.UDPConn 关掉。
func TestListenUDPReleasesPortWhenSetsockoptFails(t *testing.T) {
	port := common.PickPort("udp", "127.0.0.1")
	laddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}

	_, err := ListenUDP("udp", laddr)
	if !setsockoptFailure(t, err) {
		return
	}

	retry, retryErr := net.ListenUDP("udp", laddr)
	if retryErr != nil {
		t.Fatalf("ListenUDP 失败后端口仍被占用，socket 泄露：%v", retryErr)
	}
	retry.Close() //gosec:disable -- 错误忽略：测试清理
}
