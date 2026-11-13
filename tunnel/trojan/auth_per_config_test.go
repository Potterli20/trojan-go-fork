package trojan

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

// custom 模式下同一个进程里可以有多个 TROJAN 节点。认证器是包级共享的，
// 旧实现只看"Auth 是否非 nil"，第二个节点配置的密码列表被静默吞掉：
// 它拿到的认证器只认第一个节点的密码。
func TestAuthPerConfigIsolation(t *testing.T) {
	Auth = nil // 从干净状态开始

	ctxA := config.WithConfig(context.Background(), memory.Name, &memory.Config{Passwords: []string{"password-a"}})
	ctxB := config.WithConfig(context.Background(), memory.Name, &memory.Config{Passwords: []string{"password-b"}})

	first := newAuthTestServer(t, ctxA)
	t.Cleanup(func() { first.Close() }) //gosec:disable -- 错误忽略：测试清理
	second := newAuthTestServer(t, ctxB)
	t.Cleanup(func() { second.Close() }) //gosec:disable -- 错误忽略：测试清理

	if first.authRef == second.authRef {
		t.Fatal("配置不同的两个 Server 共用同一个认证器实例")
	}

	hashA := common.SHA224String("password-a")
	hashB := common.SHA224String("password-b")
	if ok, _ := second.authRef.auth.AuthUser(hashB); !ok {
		t.Fatal("第二个节点拒绝了自己的密码")
	}
	if ok, _ := second.authRef.auth.AuthUser(hashA); ok {
		t.Fatal("第二个节点接受了第一个节点的密码：配置被静默吞掉")
	}
	if ok, _ := first.authRef.auth.AuthUser(hashA); !ok {
		t.Fatal("第一个节点拒绝了自己的密码")
	}
}