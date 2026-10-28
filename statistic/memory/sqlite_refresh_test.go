//go:build linux && (amd64 || 386 || arm || arm64)

package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/sqlite"
)

// setRefreshIntervalForTest 把刷新间隔调小，测试结束恢复默认（0 表示取默认值）。
func setRefreshIntervalForTest(t *testing.T, d time.Duration) {
	t.Helper()
	previous := sqliteRefreshInterval.Swap(int64(d))
	t.Cleanup(func() { sqliteRefreshInterval.Store(previous) })
}

// panelSetExpire 模拟面板/SQL 直接改库里这一行的 expire（其余列原样写回）。
func panelSetExpire(t *testing.T, path, hash string, expire int64) {
	t.Helper()
	pst, err := sqlite.NewSqlitePersistencer(path)
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	defer pst.Close() //gosec:disable -- 错误忽略：测试清理
	row, err := pst.LoadUser(hash)
	if err != nil {
		t.Fatalf("LoadUser(%s): %v", hash[:8], err)
	}
	user, ok := row.(*sqlite.User)
	if !ok {
		t.Fatalf("库里读出来的不是 *sqlite.User：%T", row)
	}
	user.Expire = expire
	if err := pst.SaveUser(user); err != nil {
		t.Fatalf("写回 expire: %v", err)
	}
}

// waitUntil 有界轮询，命中返回 true。
func waitUntil(bound time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestPanelExpiryAppliesWithoutRestart 钉住「改库免重启生效」。
//
// SQLite 后端原先只在 NewAuthenticator 装载时读一次 expire，于是面板把某行改成到期之后
// 认证照样通过、续期也照样不回来，只能重启进程——配置被接受而实际不起作用，按本项目口径算
// 缺陷（AGENTS.md 2026-10-27「接受但不生效」那条）。现在由 limitsRefresher 每轮 ListUser
// 把 expire / IP 上限 / 速度上限重新推进内存用户。
//
// 这条判据跑真实 sqlite 文件与真实墙钟（两个后端句柄并发读写，间隔取 100ms 而不是更小，
// 避免 SQLITE_BUSY 把轮次推迟成 flaky），判定看的是"到期"与"续期"两个方向都能翻过来。
func TestPanelExpiryAppliesWithoutRestart(t *testing.T) {
	setRefreshIntervalForTest(t, 100*time.Millisecond)
	password := "refresh-expire-password"
	path := filepath.Join(t.TempDir(), "users.db")
	cfg := &Config{Passwords: []string{password}, Sqlite: path}
	auth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, cfg))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	t.Cleanup(func() { auth.Close() }) //gosec:disable -- 错误忽略：测试清理
	hash := common.SHA224String(password)

	if valid, _ := auth.AuthUser(hash); !valid {
		t.Fatal("起始状态就该能认证，判据失效")
	}

	panelSetExpire(t, path, hash, 1) // Unix 秒 1 = 早已到期
	if !waitUntil(5*time.Second, func() bool {
		valid, _ := auth.AuthUser(hash)
		return !valid
	}) {
		t.Fatal("库里 expire 改成已到期之后认证仍然通过：刷新轮没生效，只能靠重启")
	}

	panelSetExpire(t, path, hash, 0) // 0 = 永不过期
	if !waitUntil(5*time.Second, func() bool {
		valid, _ := auth.AuthUser(hash)
		return valid
	}) {
		t.Fatal("库里 expire 续期之后认证仍然被拒：刷新轮只做了单向采纳")
	}
}

// TestPanelExpiryNotAppliedWhenSuppressed 钉住上层那两道边界之一：MySQL 内嵌 memory 认证器
// 时必须关掉这条刷新轮，否则 sqlite 的镜像行（expire=0，由 AddUser 顺手写进去）会把 MySQL
// 里已到期的人复活成永不过期。
func TestPanelExpiryNotAppliedWhenSuppressed(t *testing.T) {
	setRefreshIntervalForTest(t, 100*time.Millisecond)
	password := "refresh-suppressed-password"
	path := filepath.Join(t.TempDir(), "users.db")
	cfg := &Config{Passwords: []string{password}, Sqlite: path}
	auth, err := NewAuthenticator(WithoutPersistencerRefresh(config.WithConfig(context.Background(), Name, cfg)))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	t.Cleanup(func() { auth.Close() }) //gosec:disable -- 错误忽略：测试清理
	hash := common.SHA224String(password)

	if valid, _ := auth.AuthUser(hash); !valid {
		t.Fatal("起始状态就该能认证，判据失效")
	}
	panelSetExpire(t, path, hash, 1)
	time.Sleep(600 * time.Millisecond) // 相当于 6 轮
	if valid, _ := auth.AuthUser(hash); !valid {
		t.Fatal("上层已经声明由自己每轮推这批列，刷新轮却还是起来了：两个后端会互相盖值")
	}
	// 关掉轮子不等于内存里的值不能改：走认证器自己的入口仍然立刻生效。
	inner, ok := auth.(*Authenticator) // NewAuthenticator 交回的是接口
	if !ok {
		t.Fatalf("拿不到具体类型 *Authenticator：%T", auth)
	}
	if err := inner.SetUserExpire(hash, 1); err != nil {
		t.Fatalf("SetUserExpire: %v", err)
	}
	if valid, _ := auth.AuthUser(hash); valid {
		t.Fatal("SetUserExpire 之后还放行：AuthUser 的到期判定被改坏了")
	}
}

// TestRefreshDoesNotTouchQuota 是「绝不碰 quota」这条边界：SQLite 的表结构里没有额度列，
// 刷新轮要是把 quotaChecked 打开，就等于给所有 SQLite 用户设了 quota=0（无额度）——
// 用户会在刷新轮跑起来的那一刻被全量拒掉，而这正是 AGENTS.md 里"拿零值当没有额度概念"的坑。
func TestRefreshDoesNotTouchQuota(t *testing.T) {
	setRefreshIntervalForTest(t, 50*time.Millisecond)
	password := "refresh-quota-password"
	path := filepath.Join(t.TempDir(), "users.db")
	cfg := &Config{Passwords: []string{password}, Sqlite: path}
	auth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, cfg))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	t.Cleanup(func() { auth.Close() }) //gosec:disable -- 错误忽略：测试清理
	hash := common.SHA224String(password)

	// 先跑出一段流量，让 batchTrafficUpdater 与刷新轮都可能碰到这个用户
	_, user := auth.AuthUser(hash)
	user.AddSentTraffic(4096)
	user.AddRecvTraffic(4096)

	time.Sleep(700 * time.Millisecond) // 十几轮
	if valid, _ := auth.AuthUser(hash); !valid {
		t.Fatal("刷新轮把 SQLite 用户当成「额度为 0」拒掉了：quota 不是 SQLite 的概念，不该被碰")
	}
}
