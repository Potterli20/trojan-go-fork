package service

import (
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
)

// 回归防护：Add 分支的半成品回滚只在"本次调用确实创建了用户"时才合法。
// 若 AddUser 本身就失败（最常见：用户已存在，幂等脚本重复 Add），
// 走到公共错误分支无条件 DelUser 会把那个已配置好的用户删掉——
// SQLite 后端下是真实的 DELETE FROM users，重启都回不来。
// 这是 commit 833636bb 引入回滚后必须钉住的反面用例。
func TestSetUsersAddFailureDoesNotDeleteExistingUser(t *testing.T) {
	auth := &rollbackAuth{users: make(map[string]bool)}
	api := &ServerAPI{auth: auth}
	const hash = "existing-user"
	// 口令值无关紧要，只要非空即可；现场生成，避免源码里的固定凭据形状。
	password := common.SHA224String(t.Name())

	// 先建好一个用户（模拟已配置完成的状态）。
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("预置 AddUser: %v", err)
	}
	beforeDeleted := len(auth.deleted)

	// 对同一个 hash 再发一次 Add：AddUser 应当报 already exist。
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{Operation: SetUsersRequest_Add, Status: &UserStatus{User: &User{
			Hash: hash, Password: password,
		}}},
	}}
	if err := api.SetUsers(stream); err != nil {
		t.Fatalf("SetUsers: %v", err)
	}
	if len(stream.sent) != 1 || stream.sent[0].Success {
		t.Fatal("重复 Add 应当回 Success:false（already exist）")
	}
	if !auth.users[hash] {
		t.Fatal("重复 Add 失败后，已存在的用户被回滚删掉了（SQLite 部署下等于删库）")
	}
	if len(auth.deleted) != beforeDeleted {
		t.Fatalf("AddUser 本身失败时不应调用 DelUser，实际多删了 %d 次", len(auth.deleted)-beforeDeleted)
	}
}
