package service

import "testing"

// proto3 的枚举是开放枚举：客户端可以发任意数值的 operation，grpc-go 照常投递到 handler。
// SetUsers 的 switch 只有 Add/Delete/Modify 三个 case、没有 default，未知 operation 时
// err 保持 nil，走到末尾回 Success:true —— 服务端什么都没做却明确告知调用方成功，
// 内置 -api set 与第三方脚本据此判定改用户成功了。
//
// auth 故意留 nil：未知 operation 应当在 switch 的 default 里直接报错，不得触碰 auth；
// 万一旧实现或未来改动调了任何 auth 方法，nil 接口会 panic，判据当场变红。
func TestSetUsersRejectsUnknownOperation(t *testing.T) {
	api := &ServerAPI{}
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{
			Operation: SetUsersRequest_Operation(99),
			Status: &UserStatus{User: &User{
				Hash: "hash-for-unknown-op",
			}},
		},
	}}

	if err := api.SetUsers(stream); err != nil {
		t.Fatalf("SetUsers 不应直接返回传输错误，而应发 Success:false：%v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("期望一条回包，实际 %d 条", len(stream.sent))
	}
	if stream.sent[0].Success {
		t.Fatal("未知 operation 什么都没做，却回了 Success:true：调用方无法区分成功与操作被吞")
	}
}
