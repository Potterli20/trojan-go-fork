package recorder

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
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

// maxSubscribers 限制同时存活的订阅者数量。`api/service` 的 GetRecords 每条流现生成一个
// UUID 当 uid，所以条目数 = 并发尾随流数，而 **gRPC API 自身没有鉴权**：任何能连上 API
// 的客户端开 N 条流，就是 N × 最多 0.5MB 的缓冲加 N 个协程。maxSubscriberCapacity 管住了
// 单个订阅者的缓冲，管不住数量——这一条管数量。
//
// 超出上限时 Subscribe 返回错误而不是静默丢记录：调用方（API handler）要能把它变成一条
// 明确的 gRPC 错误，而不是让客户端以为在尾随、其实一条都收不到。
const maxSubscribers = 64

// subscribeMu 让"查计数 + 登记"成为一步。只靠 subscriberCount.Load() 判断的话，
// 并发的 Subscribe 可以一起挤过闸门，上限就成了软约束。
var subscribeMu sync.Mutex

// subscriberCount 只为 Add 的空订阅短路存在：sync.Map 没有 O(1) 的"是否为空"，
// 而 Range 一遍要分配迭代器。计数由 Swap/LoadAndDelete 的返回值维护，
// 同一 uid 重复 Subscribe 不会重复计数。
var subscriberCount atomic.Int64

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
	// 没有订阅者就直接返回：Add 是**每连接、每 UDP 包**都会走的路径
	// （tunnel/trojan/packet.go 的 Record），而下面这几行要先付 2 次 addr.String()
	// + 2 次 SplitHostPort + 时间戳 + Record 组装，才发现没人在听。
	// 绝大多数部署根本不开 record，这部分是纯白付的。
	if subscriberCount.Load() == 0 {
		return
	}
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

// Subscribe 登记一个尾随订阅者，返回它的记录通道。超出 maxSubscribers 时返回错误，
// 调用方必须把错误如实上报（不要退化成一个永远收不到记录的通道）。
//
// 同一 uid 重复订阅同样返回错误，而不是就地替换：旧实现用 Swap 换掉表项，旧通道
// 再没有任何人关闭，原调用方会永久阻塞在接收上（通道不关 = 等不到零值/ok=false），
// 而它还占着一条自己的 goroutine 与缓冲。唯一的生产调用方（api/service 的
// GetRecords）每条流现生成 UUID，正常情况下不会撞上这里。
func Subscribe(uid string, transport, targetPort string, includePayload bool) (chan Record, error) {
	subscribeMu.Lock()
	defer subscribeMu.Unlock()
	if subscriberCount.Load() >= maxSubscribers {
		log.Warn("recorder subscriber limit reached, rejecting", uid)
		return nil, common.NewError("recorder subscriber limit reached")
	}
	log.Debug("New recorder subscriber", uid)
	opt := option{
		recordChan:     make(chan Record, subscriberCapacity.Load()),
		transport:      transport,
		targetPort:     targetPort,
		includePayload: includePayload,
	}
	if _, loaded := subscribers.LoadOrStore(uid, opt); loaded {
		log.Warn("recorder subscriber already exists, rejecting", uid)
		return nil, common.NewError("recorder subscriber already exists")
	}
	subscriberCount.Add(1)
	return opt.recordChan, nil
}

func Unsubscribe(uid string) {
	log.Debug("Delete recorder subscriber", uid)
	if val, ok := subscribers.LoadAndDelete(uid); ok {
		subscriberCount.Add(-1)
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
