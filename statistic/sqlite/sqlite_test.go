//go:build linux && (amd64 || 386 || arm || arm64)

package sqlite

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/Potterli20/trojan-go-fork/statistic"
)

func newTestPersistencer(t *testing.T) *Persistencer {
	t.Helper()
	p, err := NewSqlitePersistencer(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatalf("NewSqlitePersistencer: %v", err)
	}
	t.Cleanup(func() {
		// 测试 sql.DB.Close 幂等，已关闭的用例在这里重复关闭是安全的
		if err := p.Close(); err != nil {
			t.Errorf("cleanup Close: %v", err)
		}
	})
	return p
}

func testUser(hash, password string, sent, recv uint64) *User {
	u := &User{
		Hash:      hash,
		Password:  password,
		MaxIPNum:  3,
		SendLimit: 1024,
		RecvLimit: 2048,
		Sent:      make([]byte, 8),
		Recv:      make([]byte, 8),
	}
	u.setSent(sent)
	u.setRecv(recv)
	return u
}

func TestSqlitePersistencerRoundTrip(t *testing.T) {
	p := newTestPersistencer(t)

	if err := p.SaveUser(testUser("hash-a", "password-a", 100, 200)); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	// 同一 hash 再写必须是更新而不是主键冲突
	if err := p.SaveUser(testUser("hash-a", "password-a", 300, 400)); err != nil {
		t.Fatalf("SaveUser upsert: %v", err)
	}
	if err := p.SaveUser(testUser("hash-b", "password-b", 1, 2)); err != nil {
		t.Fatalf("SaveUser second user: %v", err)
	}

	loaded, err := p.LoadUser("hash-a")
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if loaded.GetHash() != "hash-a" || loaded.GetKeyShare() != "password-a" {
		t.Fatalf("loaded identity mismatch: %v / %v", loaded.GetHash(), loaded.GetKeyShare())
	}
	if sent, recv := loaded.GetTraffic(); sent != 300 || recv != 400 {
		t.Fatalf("traffic = %d/%d, want 300/400", sent, recv)
	}
	if send, recv := loaded.GetSpeedLimit(); send != 1024 || recv != 2048 {
		t.Fatalf("speed limit = %d/%d, want 1024/2048", send, recv)
	}
	if loaded.GetIPLimit() != 3 {
		t.Fatalf("ip limit = %d, want 3", loaded.GetIPLimit())
	}

	if err := p.UpdateUserTraffic("hash-b", 7, 8); err != nil {
		t.Fatalf("UpdateUserTraffic: %v", err)
	}
	updated, err := p.LoadUser("hash-b")
	if err != nil {
		t.Fatalf("LoadUser after traffic update: %v", err)
	}
	if sent, recv := updated.GetTraffic(); sent != 7 || recv != 8 {
		t.Fatalf("traffic = %d/%d, want 7/8", sent, recv)
	}

	var hashes []string
	if err := p.ListUser(func(hash string, u statistic.Metadata) bool {
		hashes = append(hashes, hash)
		return true
	}); err != nil {
		t.Fatalf("ListUser: %v", err)
	}
	sort.Strings(hashes)
	if len(hashes) != 2 || hashes[0] != "hash-a" || hashes[1] != "hash-b" {
		t.Fatalf("ListUser returned %v, want [hash-a hash-b]", hashes)
	}

	if err := p.DeleteUser("hash-a"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := p.LoadUser("hash-a"); err == nil {
		t.Fatal("deleted user is still loadable")
	}
}

// Close 必须真正释放数据库句柄：关闭之后再查库要失败，
// 否则 statistic 后端的「补全 Close」只是形式上的
func TestSqlitePersistencerCloseReleasesHandles(t *testing.T) {
	p := newTestPersistencer(t)
	if err := p.SaveUser(testUser("hash-c", "password-c", 0, 0)); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := p.LoadUser("hash-c"); err == nil {
		t.Fatal("database still usable after Close, handles not released")
	}
}
