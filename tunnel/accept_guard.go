package tunnel

import (
	"errors"
	"fmt"
)

// ErrAcceptPanic 是"accept 路径里冒出了 panic、已经被边界收住"的哨兵错误。
//
// 它必须与"底层不可用"区分开：panic 是**某一条连接的对端字节**把下层解析代码打崩的，
// 这一层和它的监听器都还活着，循环该记一条错误继续接纳下一条；把它当成 accept 失败
// 直接 return，就等于让一次 panic 把这个入站永久停摆——比进程崩了更难发现（没有退出码、
// 没有重启，只是再也接纳不了新连接）。
var ErrAcceptPanic = errors.New("panic recovered in the accept path")

// GuardPanic 在 fn 周围立一道 panic 边界，把 panic 转成带 ErrAcceptPanic 的错误返回。
// where 只用于错误文本（哪一层的哪一段）。
//
// 为什么需要它：全仓只有 mux/tls/trojan 三处 recover，且都在**处理单条连接的
// goroutine 里**；而 accept 循环是把 `s.underlay.AcceptConn(...)` 写在循环体里的
// （tls/server.go:143、trojan/server.go:327 等）。下层在 AcceptConn 内解析的是
// **未认证对端可控的字节**（websocket 的 http.ReadRequest + Accept、shadowsocks 的
// 密文探测、simplesocks 的 readHeader），一旦 panic，它会一路冒出 accept 协程，而
// 这个协程本身没有边界、main 也没有顶层 recover——命中一次就是整个进程没了。
// 旧文档"握手 panic 由上层 recover 兜住、进程照跑"的说法在这一点上与代码不符。
//
// panic 时的资源收尾归**抛 panic 的那一层**：它的 defer 会在栈展开时跑完
// （websocket 的 handedOff + untrackPending + 解除握手时限；shadowsocks 的
// close-on-panic 见 tunnel/shadowsocks/server.go）。这道边界只负责让循环活下来。
func GuardPanic(where string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// 不打栈：这条路径的触发频率由对端决定，带上栈等于给攻击者一个日志放大器。
			// 口径同 trojan/tls/mux 里既有的 recover（都只记 panic 值）。
			err = fmt.Errorf("%w: %v in %s", ErrAcceptPanic, r, where)
		}
	}()
	return fn()
}

// AcceptConnGuarded 是给 accept 循环用的 AcceptConn：语义与直接调用
// `underlay.AcceptConn(overlay)` 一致，只是下层冒出的 panic 会变成以
// ErrAcceptPanic 包装的 error 返回，而不是把调用方的循环一起带走。
func AcceptConnGuarded(underlay Server, overlay Tunnel) (Conn, error) {
	var conn Conn
	err := GuardPanic(fmt.Sprintf("%s.AcceptConn", overlay.Name()), func() error {
		var err error
		conn, err = underlay.AcceptConn(overlay)
		return err
	})
	return conn, err
}
