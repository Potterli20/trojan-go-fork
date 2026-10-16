package service

import (
	"io"
	"testing"

	"google.golang.org/grpc"
)

// fakeSetUsersStream 只实现 handler 真正会用的 Recv/Send；其余 grpc.ServerStream
// 方法由内嵌的 nil 接口兜着——一旦被调用就是测试自己走偏了，会panic 得明明白白。
type fakeSetUsersStream struct {
	grpc.BidiStreamingServer[SetUsersRequest, SetUsersResponse]
	reqs []*SetUsersRequest
	idx  int
	sent []*SetUsersResponse
}

func (s *fakeSetUsersStream) Recv() (*SetUsersRequest, error) {
	if s.idx < len(s.reqs) {
		r := s.reqs[s.idx]
		s.idx++
		return r, nil
	}
	return nil, io.EOF
}

func (s *fakeSetUsersStream) Send(r *SetUsersResponse) error {
	s.sent = append(s.sent, r)
	return nil
}

// TestSetUsersRejectsStatusWithoutUser 钉住一条远程可触发 panic 的入口。
//
// SetUsers 原先只判 `req.Status == nil`，紧接着就取 `req.Status.User.Hash`。
//
//	protobuf 里 Status 与 User 是两个独立可选字段，所以 `SetUsers{Status:{}}`
//
// 是一条完全合法的消息：解引用 nil 指针 → panic。grpc-go 默认不 recover handler
// 里的 panic（不挂 RecoveryHandler 时直接冒到传输层），于是一条 API 消息就能把
// 整个进程打挂——而 API 端口在不少部署里是"内网即可达"。
// 同文件的 GetUsers 判的就是 req.User == nil，这里补齐同一层校验。
//
// 判据要求"不 panic 且返回错误"，不是"返回错误"：只断言后者的话，panic 会把
// 整个测试进程带崩，看不出是哪条回归。
func TestSetUsersRejectsStatusWithoutUser(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SetUsers 对 Status.User 为空的请求直接 panic（会打挂整个进程）：%v", r)
		}
	}()

	api := &ServerAPI{} // auth 留 nil：这条路径必须在碰到 auth 之前就返回
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{Operation: SetUsersRequest_Add, Status: &UserStatus{}}, // User 缺失
	}}

	if err := api.SetUsers(stream); err == nil {
		t.Fatal("Status.User 为空的请求被当成合法请求处理了")
	}
}

// TestSetUsersStillRejectsNilStatus 保证补判空没有把原来的那层校验换掉。
func TestSetUsersStillRejectsNilStatus(t *testing.T) {
	api := &ServerAPI{}
	stream := &fakeSetUsersStream{reqs: []*SetUsersRequest{
		{Operation: SetUsersRequest_Add}, // Status 整个缺失
	}}
	if err := api.SetUsers(stream); err == nil {
		t.Fatal("Status 为空的请求应当被拒绝")
	}
}
