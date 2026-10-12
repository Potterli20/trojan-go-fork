package memory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/sqlite"
)

// TestAddUserRejectsEmptyPasswordHash 关掉的是启动校验**管不到的那条旁路**：
// 空密码可以走运行时 API 进来。api/service 的 SetUsers 里是
// `if Hash == "" { Hash = SHA224String(Password) }` 然后 `auth.AddUser(Hash)` ⇒
// `-api set -add-profile -target-password ""` 会在跑着的进程上现场造出一个
// 哈希为 SHA224("")（公开常量）的用户。gRPC API 本身没有任何鉴权，
// 所以这比配置文件那条更糟：能连上 API 端口的人就能开这条免密通道。
//
// 只在 AddUser 这个收口点拦，就不止覆盖了 API 这一条调用路径。
func TestAddUserRejectsEmptyPasswordHash(t *testing.T) {
	auth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, &Config{}))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	defer auth.Close() //gosec:disable -- 错误忽略：测试清理

	const good = "runtime-added-password-0123456789abcdef"
	if err := auth.AddUser(common.SHA224String(good)); err != nil {
		t.Fatalf("正常哈希应当能添加（否则这条判据就成了永远拒绝）：%v", err)
	}
	if valid, _ := auth.AuthUser(common.SHA224String(good)); !valid {
		t.Fatal("正常添加的用户没能通过认证")
	}

	if err := auth.AddUser(common.SHA224String("")); err == nil {
		t.Fatal(`AddUser(SHA224("")) 被接受了：运行时可以现场开出免密用户`)
	}
	if err := auth.AddUser(""); err == nil {
		t.Fatal("AddUser(\"\") 被接受了：空哈希同样是不合法身份")
	}
	if valid, _ := auth.AuthUser(common.SHA224String("")); valid {
		t.Fatal(`空串的哈希竟然是有效用户`)
	}
}

// TestNewAuthenticatorSkipsPersistedEmptyPasswordUser 覆盖第二条残留路径：
// 空密码用户可能**已经躺在 sqlite 库里**（老版本二进制在 AddUser 与 SetKeyShare
// 之间崩溃就会留下空密码行——memory.go 里那段注释记录的正是这件事）。
// 启动时重新装载这些行不能把它变成可用用户。
func TestNewAuthenticatorSkipsPersistedEmptyPasswordUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.db")
	pst, err := sqlite.NewSqlitePersistencer(path)
	if err != nil {
		t.Skipf("本平台没有 sqlite 驱动: %v", err)
	}
	emptyHash := common.SHA224String("")
	u := &sqlite.User{Hash: emptyHash, Sent: make([]byte, 8), Recv: make([]byte, 8)}
	if err := pst.SaveUser(u); err != nil {
		t.Fatalf("预置空密码行失败: %v", err)
	}

	auth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, &Config{Sqlite: path}))
	if err != nil {
		t.Fatalf("带 sqlite 后端的认证器创建失败: %v", err)
	}
	defer auth.Close() //gosec:disable -- 错误忽略：测试清理

	if valid, _ := auth.AuthUser(emptyHash); valid {
		t.Fatal("库里预置的空密码用户被装载成了可用用户")
	}
	if err := auth.AddUser(common.SHA224String("still-fine-password-0123456789ab")); err != nil {
		t.Fatalf("装载一条坏记录之后正常用户也加不进来了（应当只跳过坏行）：%v", err)
	}
}
