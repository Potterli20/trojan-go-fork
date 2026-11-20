//go:build linux && (amd64 || 386 || arm || arm64)

package sqlite

import (
	"testing"
)

// 面板/运维直接 INSERT 一行只写 hash/password 的用户是明确支持的部署形态
// （limitsRefresher 每轮读库改限额就是为这种操作设计的）。gorm 把 NULL 列扫进
// User.Sent/Recv []byte 得到 nil，而 getSent/getRecv 直接 binary.BigEndian.Uint64(nil)
// ——越界 panic。NewAuthenticator 在启动同步路径调用它，无 recover ⇒ 整个进程启动失败。
func TestMalformedTrafficColumnsDoNotPanic(t *testing.T) {
	for _, name := range []string{"nil", "short"} {
		t.Run(name, func(t *testing.T) {
			u := &User{Sent: nil, Recv: nil}
			if name == "short" {
				u.Sent, u.Recv = []byte{1, 2, 3}, []byte{4, 5}
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("损坏的流量列导致 panic：%v", r)
				}
			}()
			sent, recv := u.GetTraffic()
			if sent != 0 || recv != 0 {
				t.Fatalf("损坏列应当按零流量处理，实际 sent=%d recv=%d", sent, recv)
			}
		})
	}
}
