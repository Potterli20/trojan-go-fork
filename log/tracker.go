package log

import (
	"fmt"
	"maps"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var connIDCounter atomic.Uint64

// generateConnID 生成 "conn-<纳秒尾数>-<自增序号>" 形式的会话标识。
//
// 输出与旧实现的 fmt.Sprintf("conn-%d-%d", ...) 逐字节一致，改成手工拼字节只是为了绕开
// fmt：那一版实测 220 ns/op、40 B/op、2 allocs/op（本机 Ryzen 5 4500U，go1.27.1），
// 单独占掉 NewConnectionTracker 总成本的六成。而这个调用频率是**每条连接、每个 mux 流
// 各一次**，tls/quic/websocket 也都在调。
//
// 数字一律走 strconv.AppendInt 追加到栈上的 scratch：最大长度是 5("conn-") + 6(纳秒尾数
// 上限 999999) + 1('-') + 20(uint64 十进制上限) = 32 字节，取 40 留余量，容量够就
// 不会触发 append 扩容，最终只剩 string(buf) 那一次分配。改小这个 cap 会让它在长 id
// 下悄悄退回堆分配。
func generateConnID() string {
	id := connIDCounter.Add(1)
	var scratch [40]byte
	buf := append(scratch[:0], "conn-"...)
	buf = strconv.AppendInt(buf, time.Now().UnixNano()%1000000, 10)
	buf = append(buf, '-')
	buf = strconv.AppendInt(buf, int64(id), 10)
	return string(buf)
}

type ConnectionTracker struct {
	connID    string
	module    string
	action    string
	startTime time.Time
	// mu 保护 endTime 与 fields：Success/Error（连接建立方调用）与 Destroy（消费方
	// 关闭连接时调用）可能从不同 goroutine 并发触达同一个 tracker。
	mu      sync.Mutex
	endTime time.Time
	fields  map[string]any
}

func NewConnectionTracker(module, action string) *ConnectionTracker {
	return &ConnectionTracker{
		connID:    generateConnID(),
		module:    module,
		action:    action,
		startTime: time.Now(),
	}
}

func NewConnectionTrackerWithID(connID, module, action string) *ConnectionTracker {
	return &ConnectionTracker{
		connID:    connID,
		module:    module,
		action:    action,
		startTime: time.Now(),
	}
}

func (t *ConnectionTracker) ConnID() string {
	return t.connID
}

func (t *ConnectionTracker) StartTime() time.Time {
	return t.startTime
}

func (t *ConnectionTracker) WithField(key string, value any) *ConnectionTracker {
	t.mu.Lock()
	// fields 延迟分配：tracker 是每条连接、每条 mux 流各一个（mux/client.go 一条流
	// 就用两个），而 Success/Error 的打印在 ShouldLog 不通过时整段跳过——
	// 默认级别下那个 map 是纯白付的（一个空 map 也要 48B 头部 + 一个桶）。
	if t.fields == nil {
		t.fields = make(map[string]any)
	}
	t.fields[key] = value
	t.mu.Unlock()
	return t
}

func (t *ConnectionTracker) WithFields(fields map[string]any) *ConnectionTracker {
	t.mu.Lock()
	if t.fields == nil {
		t.fields = make(map[string]any)
	}
	maps.Copy(t.fields, fields)
	t.mu.Unlock()
	return t
}

// sanitizedFields 把 fields 转成**已经清洗过的字符串**再交给日志的 %v。
// 调用方必须已持有 t.mu（Success/Error 都在锁内）。
//
// 为什么不能直接把 map 递进去：SanitizeLogInput 只改写 string 与 error 两种实参，
// map 两个分支都不匹配，于是原样穿过 fmt 落到行缓冲；golog 那第二轮自清洗用的是
// 同一个过滤，两道同样漏。这不是理论问题——tunnel/trojan/client.go 用
// WithField("user", c.user.GetHash()) 塞进去的是 SHA224，而 Trojan 的线上认证物就是这个
// hash（拿到它等于拿到这个用户），默认级别又是 InfoLevel，于是每条出站连接都在日志里
// 明文漏一次凭据。走 string 实参的同类调用（log.Info("Using user hash:", …)）本来就被
// 遮成 [REDACTED]，漏的只有 map 这一条路。
//
// 为什么不在 SanitizeLogInput 里加 default 分支一刀切：同一个 Logf 调用里 durationMs
// 走 %.2f、sentBytes 走 %d，把所有实参一律转成字符串会让动词失配、打出
// %!f(string=…)——那是把三条数值日志一起打坏。所以这道清洗留在调用方这一侧。
func (t *ConnectionTracker) sanitizedFields() string {
	return SanitizeString(fmt.Sprint(t.fields))
}

func (t *ConnectionTracker) Success() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endTime = time.Now()
	if !ShouldLog(InfoLevel) {
		return nil
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(InfoLevel, "[%s] [conn=%s] action=%s status=success start=%s end=%s duration=%.2fms %v",
		t.module, t.connID, t.action,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, t.sanitizedFields())
	return nil
}

func (t *ConnectionTracker) Error(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endTime = time.Now()
	if !ShouldLog(ErrorLevel) {
		return err
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(ErrorLevel, "[%s] [conn=%s] action=%s status=error start=%s end=%s duration=%.2fms error=%v %v",
		t.module, t.connID, t.action,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, err, t.sanitizedFields())
	return err
}

func (t *ConnectionTracker) Destroy(reason string, sentBytes, recvBytes uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endTime = time.Now()
	if !ShouldLog(InfoLevel) {
		return
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(InfoLevel, "[%s] [conn=%s] action=destroy status=closed start=%s end=%s duration=%.2fms reason=%s sent=%d recv=%d",
		t.module, t.connID,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, reason, sentBytes, recvBytes)
}
