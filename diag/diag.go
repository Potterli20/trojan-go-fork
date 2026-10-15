// Package diag 提供可选的运行期诊断（pprof）。默认完全关闭：只有显式给出
// -diag-bind 才会监听，而且只接受回环地址。
//
// 为什么需要它：Go 1.27 把 goroutineleak 剖析正式开放给 runtime/pprof
// （src/runtime/pprof/pprof.go:194,265）。它跑一轮带泄露检测的 GC，只输出
// "阻塞在 channel/mutex 上、且没有任何活对象能引到"的协程——正是长期运行的
// 代理最需要的判据。用 runtime.NumGoroutine 数总量在真实栈上根本区分不了
// "在途会话"与"泄露"，而 /debug/pprof/goroutineleak 可以直接点名。
package diag

import (
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

// readHeaderTimeout 只用来限制"连上不发请求"的占用，诊断口不需要更多调优。
const readHeaderTimeout = 5 * time.Second

// NewHandler 返回挂好 pprof 路由的 handler。pprof.Index 会按路径分派到
// profile/trace/symbol/cmdline 以及所有 runtime/pprof 注册的剖析类型，
// goroutineleak 是 Go 1.27 起自动在册的那一个，因此不用单独登记。
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	return mux
}

// loopbackHost 判定绑定地址是否是回环。刻意不接受空 host（":8080" 等于挂到
// 所有网卡上），也不放行任何非回环 IP：pprof 暴露的是进程内部状态——协程栈、
// 堆直方图，以及可持续占用 CPU 的 profile 采集，挂到公网等于把进程交出去。
func loopbackHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Start 同步建立监听后再后台 Serve，于是"地址不合法/端口被占"这类错误在启动
// 阶段就能拿到，而不是等代理跑起来之后静默缺席。
// 返回实际监听地址与关停函数；bind 不是回环地址时返回错误且不开任何端口。
func Start(bind string) (addr string, stop func(), err error) {
	if !loopbackHost(bind) {
		return "", nil, common.NewErrorf("diag bind address %q is not a loopback address; refusing to expose pprof", bind)
	}
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return "", nil, common.NewError("failed to listen for diagnostics").Base(err)
	}
	server := &http.Server{Handler: NewHandler(), ReadHeaderTimeout: readHeaderTimeout}
	go func() {
		if err := server.Serve(l); err != nil && err != http.ErrServerClosed {
			log.Warn("diagnostics server stopped:", err)
		}
	}()
	log.Warn("diagnostics (pprof, including Go 1.27 goroutineleak) enabled on", l.Addr().String(),
		"- it exposes internal process state, keep it loopback-only")
	return l.Addr().String(), func() {
		// Close 会连在途请求一起掐掉；诊断口不需要 Shutdown 那套等空闲的体面
		server.Close() //gosec:disable -- 错误忽略：关停路径，监听器已尽力关闭
	}, nil
}
