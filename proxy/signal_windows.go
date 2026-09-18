//go:build windows

package proxy

import "os"

// shutdownSignals 是在优雅关闭时触发代理退出的信号集合。
// Windows 下仅 Ctrl+C(os.Interrupt) 能被 signal.Notify 可靠捕获，
// syscall.SIGTERM 在 Windows 上不存在，故只监听 os.Interrupt。
var shutdownSignals = []os.Signal{os.Interrupt}
