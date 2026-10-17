package service

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Potterli20/trojan-go-fork/recorder"
)

const testToken = "s3cret-token"

// dialTokenAPI 起一个只带 token 鉴权的 API 服务端（bufconn，不占端口），
// 返回按给定 token 拨过去的客户端。token 传空串表示"完全不带 authorization 头"。
func dialTokenAPI(t *testing.T, token string) (TrojanServerServiceClient, func()) {
	t.Helper()

	cfg := &Config{API: APIConfig{Enabled: true, APIHost: "127.0.0.1", Token: testToken}}
	srv, err := newAPIServer(cfg)
	if err != nil {
		t.Fatalf("newAPIServer: %v", err)
	}
	// auth 留 nil：GetRecords 不碰它，而带 token 的正向用例只走这一条流式方法
	RegisterTrojanServerServiceServer(srv, &ServerAPI{})

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	}
	if token != "" {
		md := metadata.Pairs(TokenMetadataKey, TokenPrefix+token)
		opts = append(opts,
			grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any,
				cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
				ctx = metadata.NewOutgoingContext(ctx, md)
				return invoker(ctx, method, req, reply, cc, callOpts...)
			}),
			grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
				method string, streamer grpc.Streamer, callOpts ...grpc.CallOption) (grpc.ClientStream, error) {
				ctx = metadata.NewOutgoingContext(ctx, md)
				return streamer(ctx, desc, cc, method, callOpts...)
			}),
		)
	}
	conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
	if err != nil {
		srv.Stop()
		lis.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return NewTrojanServerServiceClient(conn), func() {
		conn.Close() //gosec:disable -- 错误忽略：测试清理
		srv.Stop()
		lis.Close() //gosec:disable -- 错误忽略：测试清理
	}
}

// TestStreamRPCRequiresToken 钉住流式方法也被拦住。GetRecords 是尾随实时记录的流，
// 只挂一元拦截器的话它照样裸奔——而它恰好是信息量最大的一条（客户端 IP、目标、
// 可选负载）。
func TestStreamRPCRequiresToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"不带 authorization 头", ""},
		{"带错 token", "wrong-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, cleanup := dialTokenAPI(t, tc.token)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.GetRecords(ctx, &GetRecordsRequest{})
			if err != nil {
				// 建流本身就失败也算拦住，只要不是"成功但收不到东西"
				if status.Code(err) != codes.Unauthenticated {
					t.Fatalf("期望 Unauthenticated，实得 %v", err)
				}
				return
			}
			_, err = stream.Recv()
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("期望 Unauthenticated，实得 %v", err)
			}
		})
	}
}

// TestStreamRPCWithTokenReceivesRecords 是正向对照：带对 token 的调用必须真的能用，
// 否则"加了鉴权"很容易变成"把 API 关掉了"。这里让服务端广播一条记录，客户端应当收到。
func TestStreamRPCWithTokenReceivesRecords(t *testing.T) {
	client, cleanup := dialTokenAPI(t, testToken)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.GetRecords(ctx, &GetRecordsRequest{IncludePayload: true})
	if err != nil {
		t.Fatalf("带正确 token 建流失败: %v", err)
	}

	recv := make(chan *GetRecordsResponse, 1)
	recvErr := make(chan error, 1)
	go func() {
		resp, err := stream.Recv()
		if err != nil {
			recvErr <- err
			return
		}
		recv <- resp
	}()

	// 订阅是在服务端 handler 里建立的，广播要等到那之后才有收件人：轮询发几条，
	// 收到即止（recorder.Add 在零订阅者时直接短路，所以早发的那几条会被丢掉）
	clientAddr := &net.TCPAddr{IP: net.IPv4(198, 51, 100, 9), Port: 4321}
	targetAddr := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 443}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recorder.Add("0123456789abcdef", clientAddr, targetAddr, "tcp", []byte("ping"))
		select {
		case resp := <-recv:
			if resp.ClientIp != "198.51.100.9" || resp.TargetPort != "443" {
				t.Fatalf("收到的记录不对: %+v", resp)
			}
			return
		case err := <-recvErr:
			t.Fatalf("带正确 token 的流被拒了: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("带正确 token 的流建起来了，但 5 秒内没有收到任何广播的记录")
}

// TestTokenUnaryInterceptor 在单元层钉住一元拦截器的判定：没有 metadata、token 不对
// 都必须在**调用 handler 之前**拒掉（handler 一旦被调到，副作用就已经发生了），
// 而带前缀与不带前缀的写法都要接受。
func TestTokenUnaryInterceptor(t *testing.T) {
	var called bool
	handler := func(context.Context, any) (any, error) {
		called = true
		return "ok", nil
	}
	intercept := tokenUnaryInterceptor(testToken)
	info := &grpc.UnaryServerInfo{}

	cases := []struct {
		name     string
		ctx      context.Context
		wantCode codes.Code
		wantCall bool
	}{
		{
			name:     "没有 metadata",
			ctx:      context.Background(),
			wantCode: codes.Unauthenticated,
		},
		{
			name: "有 metadata 但没有 authorization 头",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs("other", "x")),
			wantCode: codes.Unauthenticated,
		},
		{
			name: "token 不对",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs(TokenMetadataKey, TokenPrefix+"nope")),
			wantCode: codes.Unauthenticated,
		},
		{
			name: "带 Bearer 前缀的正确 token",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs(TokenMetadataKey, TokenPrefix+testToken)),
			wantCode: codes.OK,
			wantCall: true,
		},
		{
			name: "不带前缀的裸 token 也接受",
			ctx: metadata.NewIncomingContext(context.Background(),
				metadata.Pairs(TokenMetadataKey, testToken)),
			wantCode: codes.OK,
			wantCall: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			_, err := intercept(tc.ctx, nil, info, handler)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("状态码 = %v，期望 %v（err=%v）", got, tc.wantCode, err)
			}
			if called != tc.wantCall {
				t.Fatalf("handler 被调用 = %v，期望 %v：鉴权必须在副作用之前完成", called, tc.wantCall)
			}
		})
	}
}
