package service

import "testing"

// SetUsers(Modify) 修复前无条件执行 SetUserIPLimit(hash, IpLimit)，
// proto3 标量字段省略即 0 ⇒ 只改限速的 Modify 会把已有 IP 上限静默清零。
// ip_limit 改成 optional 后，省略与显式 0 可区分；本判据钉住：
// Modify 不带 IpLimit 时不得触碰 IP 上限，显式带 0 时才清零。
type ipLimitAuth struct {
	rollbackAuth
	ipLimits   map[string]int
	ipSetCalls int
}

func newIPLimitAuth() *ipLimitAuth {
	a := &ipLimitAuth{ipLimits: make(map[string]int)}
	a.users = make(map[string]bool)
	return a
}

func (a *ipLimitAuth) SetUserIPLimit(hash string, limit int) error {
	a.ipSetCalls++
	a.ipLimits[hash] = limit
	return nil
}

func TestSetUsersModifyOmittedIPLimitKeepsValue(t *testing.T) {
	auth := newIPLimitAuth()
	// 模拟一个已有 3 IP 上限的用户（Add 阶段显式设过）。
	if err := auth.AddUser("modify-keep-user"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := auth.SetUserIPLimit("modify-keep-user", 3); err != nil {
		t.Fatalf("SetUserIPLimit: %v", err)
	}
	before := auth.ipSetCalls

	api := &ServerAPI{auth: auth}
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{
			Operation: SetUsersRequest_Modify,
			Status: &UserStatus{
				User: &User{Hash: "modify-keep-user"},
				SpeedLimit: &Speed{
					DownloadSpeed: 100,
					UploadSpeed:   100,
				},
				// IpLimit 故意省略（nil）
			},
		},
	}}
	if err := api.SetUsers(stream); err != nil {
		t.Fatalf("SetUsers: %v", err)
	}
	if got := auth.ipLimits["modify-keep-user"]; got != 3 {
		t.Fatalf("Modify 只改限速却把 IP 上限从 3 清成了 %d：省略 ip_limit 应当保留原值", got)
	}
	if auth.ipSetCalls != before {
		t.Fatalf("省略 ip_limit 时仍调用了 SetUserIPLimit（%d 次）", auth.ipSetCalls-before)
	}
}

func TestSetUsersModifyExplicitZeroClearsIPLimit(t *testing.T) {
	auth := newIPLimitAuth()
	if err := auth.AddUser("modify-clear-user"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := auth.SetUserIPLimit("modify-clear-user", 3); err != nil {
		t.Fatalf("SetUserIPLimit: %v", err)
	}

	zero := int32(0)
	api := &ServerAPI{auth: auth}
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{
			Operation: SetUsersRequest_Modify,
			Status: &UserStatus{
				User:    &User{Hash: "modify-clear-user"},
				IpLimit: &zero,
			},
		},
	}}
	if err := api.SetUsers(stream); err != nil {
		t.Fatalf("SetUsers: %v", err)
	}
	if got := auth.ipLimits["modify-clear-user"]; got != 0 {
		t.Fatalf("显式 ip_limit=0 应当清零，实际 %d", got)
	}
}
