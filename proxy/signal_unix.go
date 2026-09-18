//go:build !windows

package proxy

import (
	"os"
	"syscall"
)

// shutdownSignals 是在优雅关闭时触发代理退出的信号集合。
// Unix 类系统同时监听 SIGINT(Ctrl+C) 与 SIGTERM(服务管理器/kill 默认信号)。
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
