package service

import (
	"fmt"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

// SetUsers(Add) 在 AddUser 成功之后、SetKeyShare 失败时直接 break 回 Success:false，
// 但没有任何回滚：半成品用户（无密码）留在内存表里，后台协程照常跑。运维修好 DB
// 后重试同一个 Add，AddUser 命中"hash already exist"永远失败——只能手工 Delete 一次。
// 判据用 fake auth 复现：第一次 SetKeyShare 失败、第二次同名 Add 必须能成功。
type rollbackAuth struct {
	users         map[string]bool
	keyShareFails int
	deleted       []string
}

func (a *rollbackAuth) AuthUser(string) (bool, statistic.User) { return false, nil }
func (a *rollbackAuth) AddUser(hash string) error {
	if a.users[hash] {
		return fmt.Errorf("hash %s already exist", hash)
	}
	a.users[hash] = true
	return nil
}
func (a *rollbackAuth) DelUser(hash string) error {
	delete(a.users, hash)
	a.deleted = append(a.deleted, hash)
	return nil
}
func (a *rollbackAuth) SetKeyShare(hash, pwd string) error {
	if a.keyShareFails > 0 {
		a.keyShareFails--
		return fmt.Errorf("database is locked")
	}
	return nil
}
func (a *rollbackAuth) SetUserTraffic(string, uint64, uint64) error { return nil }
func (a *rollbackAuth) SetUserSpeedLimit(string, int, int) error    { return nil }
func (a *rollbackAuth) SetUserIPLimit(string, int) error            { return nil }
func (a *rollbackAuth) ListUsers() []statistic.User                 { return nil }
func (a *rollbackAuth) Close() error                                { return nil }

func TestSetUsersAddRollsBackHalfCreatedUser(t *testing.T) {
	auth := &rollbackAuth{users: make(map[string]bool), keyShareFails: 1}
	api := &ServerAPI{auth: auth}
	const hash = "rollback-test-user"
	// 口令值无关紧要，只要非空即可；现场生成，避免源码里的固定凭据形状。
	password := common.SHA224String(t.Name())

	add := func() bool {
		stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
			{Operation: SetUsersRequest_Add, Status: &UserStatus{User: &User{
				Hash: hash, Password: password,
			}}},
		}}
		if err := api.SetUsers(stream); err != nil {
			t.Fatalf("SetUsers: %v", err)
		}
		if len(stream.sent) != 1 {
			t.Fatalf("期望一条回包，实际 %d", len(stream.sent))
		}
		return stream.sent[0].Success
	}

	if add() {
		t.Fatal("第一次 Add 的 SetKeyShare 失败，应当回 Success:false")
	}
	if !add() {
		t.Fatal("第一次 Add 失败后没有回滚半成品用户，第二次同名 Add 永久报 already exist——运维必须手工删库才能补齐")
	}
}
