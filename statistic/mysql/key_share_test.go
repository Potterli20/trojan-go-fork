package mysql

import "testing"

// MySQL 同步每轮对每个库行调 SetKeyShare(hash, username)，把人类可读的
// username 当成"共享口令"写进内存用户的 password 字段。这个调用来自 2023 年的
// commit 081672fc "Set Username as password (IP Limit Bug)"，当时是为绕开
// 某个 IP limit bug 的权宜之计；今天的 writeBackTraffic 已按 hash 做 WHERE、
// 且刻意不检查 RowsAffected，不再依赖 key share 非空。
//
// 后果：同时启用 mysql 与 sqlite 双后端时，SetKeyShare 内部的 pst.SaveUser
// 会把 username 写进 sqlite 镜像行的 password 列，覆盖面板写入的真实 key share。
// trojan 认证只比 SHA224 hash、不受影响，但镜像行的凭据值被污染，且重启装载后
// 会带着这个错误值。判据：同步后内存用户的 key share 不得被写成 username。
func TestSyncUsersDoesNotOverwriteKeyShareWithUsername(t *testing.T) {
	const (
		hash     = "u-keyshare"
		username = "human-readable-name"
	)
	db := &fakeDB{rows: []fakeRow{{
		username: username, hash: hash, quota: -1,
	}}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()

	user := mustUser(t, a, hash)
	if got := user.GetKeyShare(); got == username {
		t.Fatalf("syncUsers 把 username 写进了 key share（%q）：会覆盖 SQLite 镜像行里的真实凭据", got)
	}
}
