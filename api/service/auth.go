package service

import (
	"context"
	"crypto/subtle"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Potterli20/trojan-go-fork/api"
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// TokenMetadataKey 是承载共享密钥的 gRPC metadata 键。用标准的 authorization 头而不是
// 自造键名：现成的网关/代理会转发它，而自造键在 HTTP/2 上还得担心大小写归一。
const TokenMetadataKey = "authorization"

// TokenPrefix 与 RFC 6750 一致；也接受不带前缀的裸 token（CLI 之外的客户端少踩一个坑）。
const TokenPrefix = "Bearer "

// checkAPIExposure 决定"这个 API 能不能按当前配置开"，在监听之前同步执行。
//
// 失败关闭：`api.enabled` 且监听地址不是回环时，必须至少有一层凭据——`api.token`，
// 或者 `api.ssl.enabled` + `verify_client`。都没有就返回错误，让建栈失败、进程以非零
// 退出码结束；`api.allow_insecure: true` 是显式逃生阀（放行但 Warn）。
//
// 为什么不能只写在 RunServerAPI/RunClientAPI 里：那两处的返回值被丢进 goroutine
// （tunnel/trojan/{server,client}.go），在那里 return error 等于什么都没发生——代理
// 照常跑、API 悄悄不在、日志一行都没有。所以这个检查同时注册成 api.ExposureChecker，
// 由 NewServer/NewClient 同步调用。
func checkAPIExposure(cfg *Config) error {
	if !cfg.API.Enabled {
		return nil
	}
	addr, err := net.ResolveIPAddr("ip", cfg.API.APIHost)
	if err != nil {
		return common.NewError("api found invalid addr").Base(err)
	}
	// APIHost 留空时 ResolveIPAddr 给出通配地址（IP 为 nil），那是"所有网卡"，
	// 必须按非回环处理——docs/configuration.md 记过这个默认形态。
	if addr.IP != nil && addr.IP.IsLoopback() {
		return nil
	}
	if cfg.API.Token != "" {
		return nil
	}
	if cfg.API.SSL.Enabled && cfg.API.SSL.VerifyClient {
		return nil
	}
	if cfg.API.AllowInsecure {
		log.Warn("api is exposed on a non-loopback address without any credential " +
			"(api.allow_insecure is set): anyone who can reach it can add/remove users " +
			"and tail traffic records")
		return nil
	}
	return common.NewError("refusing to expose the gRPC API on " + listenDescription(addr) +
		" without a credential: set api.token, or api.ssl.enabled with api.ssl.verify_client, " +
		"or bind api_addr to a loopback address; api.allow_insecure: true overrides this check")
}

// listenDescription 只给错误文案用：通配地址要说清是"所有网卡"，否则用户看不出
// 自己根本没写 api_addr。
func listenDescription(addr *net.IPAddr) string {
	if addr.IP == nil || addr.IP.IsUnspecified() {
		return "all interfaces (api_addr is empty or a wildcard)"
	}
	return addr.IP.String()
}

// authorize 校验这次 RPC 带的 token。用常量时间比较：token 是共享密钥，
// 逐字节短路比较会把长度与前缀泄露给计时侧信道。
func authorize(ctx context.Context, token string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "api requires a token: no metadata in request")
	}
	values := md.Get(TokenMetadataKey)
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "api requires a token: missing "+TokenMetadataKey)
	}
	got := strings.TrimPrefix(values[0], TokenPrefix)
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return status.Error(codes.Unauthenticated, "api token rejected")
	}
	return nil
}

func tokenUnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorize(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func tokenStreamInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorize(stream.Context(), token); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

func init() {
	checker := func(name string) api.ExposureChecker {
		return func(ctx context.Context) error {
			cfg, ok := config.FromContext(ctx, Name).(*Config)
			if !ok {
				return common.NewError("api config not found in context while checking exposure of " + name)
			}
			return checkAPIExposure(cfg)
		}
	}
	api.RegisterExposureChecker(trojan.Name+"_SERVER", checker(trojan.Name+"_SERVER"))
	api.RegisterExposureChecker(trojan.Name+"_CLIENT", checker(trojan.Name+"_CLIENT"))
}
