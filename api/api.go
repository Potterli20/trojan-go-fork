package api

import (
	"context"
	"sync"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

type Handler func(ctx context.Context, auth statistic.Authenticator) error

var (
	handlers = make(map[string]Handler)
	mu       sync.RWMutex
)

func RegisterHandler(name string, handler Handler) {
	mu.Lock()
	defer mu.Unlock()
	handlers[name] = handler
}

func RunService(ctx context.Context, name string, auth statistic.Authenticator) error {
	mu.RLock()
	h, ok := handlers[name]
	mu.RUnlock()
	if ok {
		log.Debug("api handler found", name)
		return h(ctx, auth)
	}
	log.Debug("api handler not found", name)
	return nil
}

// ExposureChecker 在**监听之前**同步判定"这个 API 能不能按当前配置开"。
//
// 与 Handler 分开注册，是因为 RunService 的返回值在两个调用点都被丢进 goroutine 里
// （tunnel/trojan/server.go 与 client.go 的 `s.wg.Go(func(){ api.RunService(...) })`），
// 在那里返回错误等于什么都不发生：代理照常跑，API 悄悄不在，日志里连一行都没有
// （docs/configuration.md 记过这个静默形态）。策略性拒绝必须发生在建栈阶段、以非零
// 退出码收场，所以它是独立的一类钩子，由 NewServer/NewClient 同步调用。
type ExposureChecker func(ctx context.Context) error

var (
	exposureCheckers = make(map[string]ExposureChecker)
	exposureMu       sync.RWMutex
)

// RegisterExposureChecker 登记一个暴露面检查器。未注册的 name 一律放行
// （比如 mini 构建里没有 api/service，CheckExposure 就不能变成硬失败）。
func RegisterExposureChecker(name string, checker ExposureChecker) {
	exposureMu.Lock()
	defer exposureMu.Unlock()
	exposureCheckers[name] = checker
}

// CheckExposure 跑指定 name 的检查器；没有登记就返回 nil。
func CheckExposure(ctx context.Context, name string) error {
	exposureMu.RLock()
	c, ok := exposureCheckers[name]
	exposureMu.RUnlock()
	if !ok {
		return nil
	}
	return c(ctx)
}
