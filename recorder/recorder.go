package recorder

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
)

// subscriberCapacity 为每个订阅者的 channel 缓冲大小。
// 运行中会被 trojan 服务端按配置覆盖，而 Subscribe（API 调用）可能并发读取，
// 因此用 atomic 而非裸全局变量。
var subscriberCapacity atomic.Int32

func init() { subscriberCapacity.Store(10) }

// maxSubscriberCapacity 是每个订阅者缓冲的上限。
//
// channel 的容量是 Subscribe 那一刻**一次性预分配**的，而 Record 有 136 字节
// （实测 reflect 值）：配 1<<20 就是每个订阅者 136MB，配到 2e9 直接
// `fatal error: runtime: out of memory` —— 那是 runtime throw，不是 panic，
// recover 抓不住，进程当场没了。而触发点是 `api/service` 的 GetRecords：
// 任何一个能打 API 的客户端调一次，就能把整个服务端弄死。
// 旧实现里"溢出就 clamp 到 MaxInt32"那条兜底恰恰是最坏的选择——它把越界值
// 换成**可能最大**的容量。
//
// 上限取 4096 条 ≈ 0.5MB/订阅者：溢出时本来就按 select+default 丢弃，
// 一个跟不上流的订阅者配多大的缓冲都一样丢。
const maxSubscriberCapacity = 4096

// SetCapacity 设置每个订阅者的缓冲容量（须在 Subscribe 之前调用才有意义）
func SetCapacity(n int) {
	if n <= 0 {
		return
	}
	if n > maxSubscriberCapacity {
		log.Warn("record_capacity", n, "is above the limit, clamped to", maxSubscriberCapacity)
		n = maxSubscriberCapacity
	}
	subscriberCapacity.Store(int32(n)) //gosec:disable -- 上方已钳制到 maxSubscriberCapacity，转换恒安全
}

var subscribers sync.Map

type option struct {
	recordChan     chan Record
	transport      string
	targetPort     string
	includePayload bool
}

type Record struct {
	Timestamp  string
	UserHash   string
	ClientIp   string
	ClientPort string
	TargetHost string
	TargetPort string
	Transport  string
	Payload    []byte
}

func Add(hash string, clientAddr, targetAddr net.Addr, transport string, payload []byte) {
	clientIP, clientPort, _ := net.SplitHostPort(clientAddr.String())
	targetHost, targetPort, _ := net.SplitHostPort(targetAddr.String())

	record := Record{
		Timestamp:  strconv.Itoa(int(time.Now().UnixMilli())), //gosec:disable -- UnixMilli 在 2927 年前远小于 MaxInt，转换安全
		UserHash:   hash,
		ClientIp:   clientIP,
		ClientPort: clientPort,
		TargetHost: targetHost,
		TargetPort: targetPort,
		Transport:  transport,
		Payload:    payload,
	}
	broadcast(record)
}

func Subscribe(uid string, transport, targetPort string, includePayload bool) chan Record {
	log.Debug("New recorder subscriber", uid)
	opt := option{
		recordChan:     make(chan Record, subscriberCapacity.Load()),
		transport:      transport,
		targetPort:     targetPort,
		includePayload: includePayload,
	}
	subscribers.Store(uid, opt)
	return opt.recordChan
}

func Unsubscribe(uid string) {
	log.Debug("Delete recorder subscriber", uid)
	if val, ok := subscribers.LoadAndDelete(uid); ok {
		opt := val.(option)
		close(opt.recordChan)
	}
}

func broadcast(record Record) {
	// 守护 send：Unsubscribe 与 broadcast 并发时，send 到已关闭 channel 会 panic，
	// select+default 无法避免 closed-channel panic。这里用 recover 兜底，
	// 让 send 到已关闭 channel 被静默丢弃（这正是我们想要的行为），不阻断进程。
	defer func() { _ = recover() }()

	payload := record.Payload

	subscribers.Range(func(uid, o any) bool {
		opt := o.(option)
		if opt.transport != "" && opt.transport != record.Transport {
			return true
		}
		if opt.targetPort != "" && opt.targetPort != record.TargetPort {
			return true
		}
		if opt.includePayload {
			buf := make([]byte, len(payload))
			copy(buf, payload)
			record.Payload = buf
		} else {
			record.Payload = nil
		}

		select {
		case opt.recordChan <- record:
		default:
		}
		return true
	})
}
