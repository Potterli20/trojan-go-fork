package control

import (
	"context"
	"fmt"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Potterli20/trojan-go-fork/api/service"
)

// stubServer 只需要把 GetRecords 演成"正常发完两条就结束了"——这正是
// server-streaming RPC 的标准收尾：客户端 Recv() 会拿到 io.EOF。
type stubServer struct {
	service.UnimplementedTrojanServerServiceServer
	records int
}

func (s *stubServer) GetRecords(_ *service.GetRecordsRequest, stream service.TrojanServerService_GetRecordsServer) error {
	for i := range s.records {
		if err := stream.Send(&service.GetRecordsResponse{
			Timestamp:  fmt.Sprint(1700000000000 + i),
			UserHash:   "deadbeef",
			ClientIp:   "127.0.0.1",
			ClientPort: "1234",
			TargetHost: "example.com",
			TargetPort: "443",
			Transport:  "TCP",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *stubServer) ListUsers(_ *service.ListUsersRequest, stream service.TrojanServerService_ListUsersServer) error {
	for i := range s.records {
		if err := stream.Send(&service.ListUsersResponse{
			Status: &service.UserStatus{User: &service.User{Hash: "deadbeef"}, IpCurrent: int32(i)},
		}); err != nil {
			return err
		}
	}
	return nil
}

func startStub(t *testing.T, records int) service.TrojanServerServiceClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	service.RegisterTrojanServerServiceServer(gs, &stubServer{records: records})
	go gs.Serve(lis) //gosec:disable -- 错误忽略：测试夹具
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return service.NewTrojanServerServiceClient(conn)
}

// TestListRecordsTreatsStreamEndAsSuccess 钉的是退出码谎报：GetRecords 是
// server-streaming，服务端正常发完之后客户端 Recv() 返回的是 io.EOF，
// 它是"流结束了"的哨兵，不是失败。原来的 listRecords 直接 return err，
// 于是 `-api record` 把记录全打出来之后还算失败。
//
// 这条在 main.go 把 handler 的错误接实之前只表现为多一行 ERROR 日志；
// 之后如果不修，就会变成"成功也 exit 1"。
func TestListRecordsTreatsStreamEndAsSuccess(t *testing.T) {
	client := startStub(t, 2)
	o := &apiController{ctx: context.Background()}
	if err := o.listRecords(client); err != nil {
		t.Fatalf("服务端正常返回 2 条记录后结束流，listRecords 却报错：%v", err)
	}
}

// TestListUsersTreatsStreamEndAsSuccess 是同一条规则的对照：listUsers 早就按
// io.EOF 收尾了，这条保证它别被"顺手统一"改坏。
func TestListUsersTreatsStreamEndAsSuccess(t *testing.T) {
	client := startStub(t, 3)
	o := &apiController{ctx: context.Background()}
	if err := o.listUsers(client); err != nil {
		t.Fatalf("listUsers 应当把流正常结束当成成功，实际报错：%v", err)
	}
}
