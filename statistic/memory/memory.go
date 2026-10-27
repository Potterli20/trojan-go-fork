package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/sqlite"
)

const Name = "MEMORY"

// ipStaleAfter 是源 IP 表项的兜底回收时限：AddIP 会刷新 lastSeen，超过这个时限没被
// 刷新的表项由 ipCleaner 强制收回，用来兜住 DelIP 漏调的情况。
const ipStaleAfter = 10 * time.Second

// ipSlot 记录一个源 IP 的引用数与最后一次被占用时刻。
// refs 是这次加上的关键字段：AddIP/DelIP 是**按连接**调用的（见 tunnel/trojan/server.go
// 的 Auth 与 Close），而计数语义是**不同源 IP 的个数**。没有 refs 时同一个 IP 的第二条
// 连接只刷新时间、不增加计数，于是其中任意一条连接关闭时的 DelIP 会把整条表项删掉、
// 计数归零，而另一条连接还活着——上限提前失效。NAT 后多个用户共用一个出口 IP 是最日常
// 的形态，一个连接池也能稳定复现。
type ipSlot struct {
	refs     int
	lastSeen time.Time
}

type User struct {
	Sent          atomic.Uint64
	Recv          atomic.Uint64
	lastSent      atomic.Uint64
	lastRecv      atomic.Uint64
	sendSpeed     atomic.Uint64
	recvSpeed     atomic.Uint64
	persistedSent atomic.Uint64 // 持久化基线，仅 batchTrafficUpdater 读写，与 speedUpdater 使用的 lastSent 物理分离
	persistedRecv atomic.Uint64 // 持久化基线，仅 batchTrafficUpdater 读写，与 lastRecv 物理分离
	Hash          string
	password      string
	passLock      sync.RWMutex
	ipTable       map[string]*ipSlot
	ipNum         atomic.Int32
	MaxIPNum      int
	limiterLock   sync.RWMutex
	ipLock        sync.Mutex
	SendLimiter   *rate.Limiter
	RecvLimiter   *rate.Limiter
	ctx           context.Context
	wg            sync.WaitGroup
	cancel        context.CancelFunc
	// expire 是到期时间（Unix 秒，<= 0 = 永不过期）。只有从持久化后端装载的用户会带值，
	// 配置里的内存用户恒为 0。用 atomic：装载时写一次，AuthUser 并发读。
	expire atomic.Int64
	// quotaChecked 为关时，下面两个字段完全不参与判定。之所以不拿 quotaLimit 的零值当"没配额"：
	// MySQL 的 users.quota 语义里 **0 = 无额度**、负数 = 不限流量，零值恰好是最严的那一档，
	// 缺省就会把所有内存用户和 SQLite 用户全部拒掉。atomic.Bool 的零值是 false，
	// 所以新增用户构造点不需要记得初始化它。
	quotaChecked atomic.Bool
	quotaLimit   atomic.Int64  // 与 MySQL users.quota 同义：0 = 无额度，< 0 = 不限流量
	quotaBase    atomic.Uint64 // 上一轮同步时库里的 download+upload（已刷新的部分）
}

func (u *User) Close() error {
	u.ResetTraffic()
	u.cancel()
	u.wg.Wait()
	return nil
}

func (u *User) ipCleaner() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case now := <-ticker.C:
			u.ipLock.Lock()
			u.sweepStaleIPs(now)
			u.ipLock.Unlock()
		}
	}
}

// sweepStaleIPs 收回超过 ipStaleAfter 没被 AddIP 刷新过的表项，调用方必须持有 ipLock。
// 这里是**强制**回收：即使 refs>0 也删。它是 DelIP 漏调时的兜底，让"引用泄漏"最坏只占
// 一个时限，而不是永久占住槽位——加引用计数的时候必须把这条留着，否则就把一个"上限偏松"
// 的问题换成了一个"槽位永不释放"的泄漏。长连接空闲过也会被收回一次，下一条新连接重新
// 计数，这是改造前就有的行为。
func (u *User) sweepStaleIPs(now time.Time) {
	for ip, slot := range u.ipTable {
		if now.Sub(slot.lastSeen) >= ipStaleAfter {
			delete(u.ipTable, ip)
			u.ipNum.Add(-1)
		}
	}
}

func (u *User) AddIP(ip string) bool {
	// MaxIPNum 会被 setIPLimit 并发写（API 的 SetUsers 与每轮 mysql syncUsers），必须
	// 与写端配对；所以“不限流快速返回”也放进锁里判——否则这里就是一处无同步的读。
	u.ipLock.Lock()
	defer u.ipLock.Unlock()

	if u.MaxIPNum <= 0 {
		return true
	}

	if slot, found := u.ipTable[ip]; found {
		// 已经有别的连接占着这个源 IP：加引用、刷新时限，**不**增加计数
		// （计数按"不同 IP 个数"算）。少一次 refs++ 就会让先关的那条连接
		// 把还在用的表项整个删掉。
		slot.refs++
		slot.lastSeen = time.Now()
		return true
	}

	if int(u.ipNum.Load()) >= u.MaxIPNum {
		return false
	}

	u.ipTable[ip] = &ipSlot{refs: 1, lastSeen: time.Now()}
	u.ipNum.Add(1)
	return true
}

func (u *User) DelIP(ip string) bool {
	u.ipLock.Lock()
	defer u.ipLock.Unlock()

	if u.MaxIPNum <= 0 {
		return true
	}

	slot, found := u.ipTable[ip]
	if !found {
		return false
	}

	slot.refs--
	// 只有最后一条连接走才真正释放槽位。refs 归零即删表项，所以
	// ipNum 恒等于 len(ipTable)，非负——重复 DelIP 拿到的是 found==false，
	// 不会把计数打到负数。
	if slot.refs <= 0 {
		delete(u.ipTable, ip)
		u.ipNum.Add(-1)
	}

	return true
}

func (u *User) GetIP() int {
	return int(u.ipNum.Load())
}

func (u *User) setIPLimit(n int) {
	// MaxIPNum 会被活跃连接的 AddIP 并发读取，必须与 ipLock 保护下的读端配对，
	// 否则 API 运行时调整 IP 上限会与 AddIP 产生数据竞争。
	u.ipLock.Lock()
	defer u.ipLock.Unlock()
	u.MaxIPNum = n
}

func (u *User) setPassword(pwd string) {
	u.passLock.Lock()
	u.password = pwd
	u.passLock.Unlock()
}

func (u *User) setExpire(expire int64) {
	u.expire.Store(expire)
}

func (u *User) GetExpire() int64 {
	return u.expire.Load()
}

// isExpired 判定到期；expire <= 0 表示永不过期（与 MySQL 端 users.quota/expire 的语义一致）。
func isExpired(expire int64) bool {
	return expire > 0 && time.Now().Unix() >= expire
}

// setQuota 登记「按时长/额度」的准入判定：limit 与 MySQL users.quota 同义（0 = 无额度，
// < 0 = 不限流量），usedBase 是同步那一刻库里已累计的 download+upload。
// 不写 persistencer：这些值来自后端本身（MySQL 每轮从 users 表读），内存用户对象只是缓存。
func (u *User) setQuota(limit int64, usedBase uint64) {
	u.quotaBase.Store(usedBase)
	u.quotaLimit.Store(limit)
	u.quotaChecked.Store(true)
}

// isOverQuota 用「库侧基线 + 内存里尚未刷库的增量」判定额度是否用尽。
// 只看库里的 download+upload 会少算当前 check_rate 周期内的流量（MySQL 是批量写回的），
// 那正好是要堵的这段窗口。基线在 writeBackTraffic 之后读，所以极少数落在这两步之间的
// 包会被计两次——偏向提前拒绝，不会反向放宽额度。
func (u *User) isOverQuota() bool {
	if !u.quotaChecked.Load() {
		return false
	}
	limit := u.quotaLimit.Load()
	if limit < 0 { // 不限流量
		return false
	}
	sent, recv := u.GetTraffic()
	used := u.quotaBase.Load() + sent + recv
	return used >= uint64(limit)
}

func (u *User) GetIPLimit() int {
	u.ipLock.Lock()
	defer u.ipLock.Unlock()
	return u.MaxIPNum
}

// waitPaced 按限速器允许的粒度把 n 个字节排队。
//
// rate.Limiter.WaitN 在 n > burst 时**立即返回错误**，而不是等待——而中继每块最大可以到
// relay_buffer_size（默认 8192，上限 1MB），burst 只有 2*limit。于是 speed_limit 低于
// 4096 B/s 的用户过去**完全不设限**，错误又被调用点丢弃，运营方以为限额生效了。
// 拆成不超过 burst 的若干次等待之后，限额才真正落到字节上。
// 剩下的唯一错误来源是 ctx 被取消（会话正在拆除），那时流量物理上已经发生，
// 账目照记，所以这里不把它当失败处理。
func waitPaced(ctx context.Context, l *rate.Limiter, n int) error {
	burst := l.Burst()
	for n > 0 {
		take := min(n, burst)
		if err := l.WaitN(ctx, take); err != nil {
			return err
		}
		n -= take
	}
	return nil
}

func (u *User) AddSentTraffic(sent int) {
	u.limiterLock.RLock()
	limiter := u.SendLimiter
	u.limiterLock.RUnlock()
	// 等待可能长时间阻塞（限速生效期间），不能持有 limiterLock 等待，
	// 否则 SetSpeedLimit 的写锁会长期饥饿，运行中修改限速迟迟不生效。
	if limiter != nil && sent >= 0 {
		waitPaced(u.ctx, limiter, sent) //gosec:disable -- 错误忽略：ctx 取消即会话正在拆除，账目仍照记
	}
	if sent >= 0 {
		u.Sent.Add(uint64(sent)) //gosec:disable -- sent >= 0 已保证安全
	}
}

func (u *User) AddRecvTraffic(recv int) {
	u.limiterLock.RLock()
	limiter := u.RecvLimiter
	u.limiterLock.RUnlock()
	if limiter != nil && recv >= 0 {
		waitPaced(u.ctx, limiter, recv) //gosec:disable -- 错误忽略：ctx 取消即会话正在拆除，账目仍照记
	}
	if recv >= 0 {
		u.Recv.Add(uint64(recv)) //gosec:disable -- recv >= 0 已保证安全
	}
}

// RestoreTraffic 把 ResetTraffic 取走、但写库失败的流量增量原样加回。
// 它是账目回滚，不是新产生的流量：刻意不经过 SendLimiter/RecvLimiter——
// 否则同一段流量会被限速两次，且可能把持久化 updater 阻塞在 WaitN 上。
func (u *User) RestoreTraffic(sent, recv uint64) {
	u.Sent.Add(sent)
	u.Recv.Add(recv)
}

func (u *User) SetSpeedLimit(send, recv int) {
	u.limiterLock.Lock()
	defer u.limiterLock.Unlock()

	if send <= 0 {
		u.SendLimiter = nil
	} else {
		u.SendLimiter = rate.NewLimiter(rate.Limit(send), send*2)
	}
	if recv <= 0 {
		u.RecvLimiter = nil
	} else {
		u.RecvLimiter = rate.NewLimiter(rate.Limit(recv), recv*2)
	}
}

func (u *User) GetSpeedLimit() (send, recv int) {
	u.limiterLock.RLock()
	defer u.limiterLock.RUnlock()

	if u.SendLimiter != nil {
		send = int(u.SendLimiter.Limit())
	}
	if u.RecvLimiter != nil {
		recv = int(u.RecvLimiter.Limit())
	}
	return
}

func (u *User) GetHash() string {
	return u.Hash
}

func (u *User) setTraffic(send, recv uint64) {
	u.Sent.Store(send)
	u.Recv.Store(recv)
}

func (u *User) GetTraffic() (uint64, uint64) {
	return u.Sent.Load(), u.Recv.Load()
}

func (u *User) ResetTraffic() (uint64, uint64) {
	sent := u.Sent.Swap(0)
	recv := u.Recv.Swap(0)
	u.lastSent.Store(0)
	u.lastRecv.Store(0)
	u.persistedSent.Store(0)
	u.persistedRecv.Store(0)
	return sent, recv
}

func (u *User) speedUpdater() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			sent, recv := u.GetTraffic()
			u.sendSpeed.Store(sent - u.lastSent.Swap(sent))
			u.recvSpeed.Store(recv - u.lastRecv.Swap(recv))
		}
	}
}

// ---------- batchTrafficUpdater 重试配置 ----------
//
// 这些值是在生产环境下的保守设定：
//   - SQLite 默认 busy_timeout 通常是 0~5 秒，
//     我们用 maxTotalBackoff≈1.2s 的退避来在应用层规避 "database is locked"，
//     同时避免单次更新阻塞整轮 10s 周期过长。
//   - 如果 4 次重试全部用完，我们在下一轮（+10s 后）再尝试，
//     此时内存中的 sent/recv 累积量依然正确，不会丢失。
const (
	// maxRetryAttempts 除首次尝试外，额外的重试次数。总尝试次数 = 1 + maxRetryAttempts。
	maxRetryAttempts = 4
	// initialBackoff 第一次重试前等待时间；后续乘以 2 指数增长。
	initialBackoff = 20 * time.Millisecond
	// maxBackoff 单次 backoff 上限（防止指数爆炸）。
	maxBackoff = 400 * time.Millisecond
)

// retryableError 判断错误是否属于"可以重试的临时错误"。
// 覆盖 SQLite("database is locked"/"is busy")、MySQL("Deadlock found"/"try restarting transaction")、
// 以及其他常见的 DB 锁错误。
//
// 子串匹配说明：
//   - "is busy" 而非 "busy"，避免误匹配 "busybox"；
//   - "lock acquired but not granted"（带空格）而非 "lock_acquiredbutnotgranted"，
//     后者无空格在真实错误字符串中不存在；
//   - "could not serialize" 而非 "serialize"，避免误匹配 "deserialize"。
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "is busy") ||
		strings.Contains(msg, "lock acquired but not granted") ||
		strings.Contains(msg, "deadlock") ||
		strings.Contains(msg, "try restarting transaction") ||
		strings.Contains(msg, "lock wait timeout") ||
		strings.Contains(msg, "could not serialize")
}

// batchTrafficUpdater 使用单个 goroutine 统一更新所有用户的流量到持久化存储，
// 避免多个 goroutine 并发写 SQLite 导致事务冲突。
//
// 每个用户的 UpdateUserTraffic 都有独立的指数退避重试（最多 maxRetryAttempts 次），
// 单个用户重试失败不会阻塞其他用户。
//
// 日志分级说明：
//   - Info:  生命周期事件（启动、退出、pst 未启用），默认可见，用于确认功能是否生效
//   - Debug: 每轮执行详情（用户数、变化列表、耗时）、每次重试细节，开 Debug 级别时可见
//   - Warn:  单个用户 UpdateUserTraffic 失败（首次失败 + 每次重试 + 重试耗尽）、失败聚合提示
func (a *Authenticator) batchTrafficUpdater() {
	if a.pst == nil {
		// 这里已经被 NewAuthenticator 的条件启动过滤了一次，
		// 再次防御性记录，防止其他入口误触发。
		log.Info("[batchTrafficUpdater] pst 为 nil，批量流量持久化未启用，goroutine 直接退出")
		return
	}

	const interval = 10 * time.Second
	startAt := time.Now()
	pstType := fmt.Sprintf("%T", a.pst)
	log.Infof("[batchTrafficUpdater] 批量流量持久化启动：interval=%s，pst=%s，"+
		"retry_max=%d，backoff=[%s→%s]。开始监听用户流量变化",
		interval, pstType, maxRetryAttempts, initialBackoff, maxBackoff)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var round uint64
	var (
		totalWrites      uint64
		totalErrors      uint64
		totalRetryHit    uint64 // 至少触发过 1 次重试的写库总次数
		totalRetryAborts uint64 // 重试全部耗尽最终放弃的总次数
	)

	for {
		select {
		case <-a.ctx.Done():
			elapsed := time.Since(startAt)
			log.Infof("[batchTrafficUpdater] 收到 ctx.Done()，批量持久化退出："+
				"运行时长=%s，总轮次=%d，总写库次数=%d，总失败次数=%d，"+
				"曾触发重试的写入=%d，重试耗尽最终放弃=%d",
				elapsed.Round(time.Millisecond), round, totalWrites, totalErrors,
				totalRetryHit, totalRetryAborts)
			return

		case <-ticker.C:
			round++
			roundStart := time.Now()

			var (
				totalUsers     uint64
				changedUsers   uint64
				successUsers   uint64
				failedUsers    uint64
				unmatchedUsers uint64
				sentBytes      uint64
				recvBytes      uint64
				retryHitUsers  uint64 // 本轮至少重试过 1 次的用户数
				retryAborted   uint64 // 本轮重试耗尽的用户数
			)

			a.users.Range(func(_, v any) bool {
				totalUsers++
				u := v.(*User)

				sent, recv := u.GetTraffic()
				lastSent := u.persistedSent.Load()
				lastRecv := u.persistedRecv.Load()

				if sent == lastSent && recv == lastRecv {
					// 流量无变化，跳过写库
					return true
				}
				changedUsers++
				deltaSent := sent - lastSent
				deltaRecv := recv - lastRecv
				sentBytes += deltaSent
				recvBytes += deltaRecv

				// ---------- 带重试的 UpdateUserTraffic ----------
				var (
					lastErr      error
					attempt      int
					retriedOnce  bool
					waitTotal    time.Duration
					waitThisTime time.Duration
				)

				for attempt = 0; attempt <= maxRetryAttempts; attempt++ {
					if attempt > 0 {
						// 指数退避 (capped)
						waitThisTime = min(initialBackoff*(1<<(attempt-1)), maxBackoff)
						waitTotal += waitThisTime

						log.Debugf("[batchTrafficUpdater] round=%d hash=%s 即将进行第 %d/%d 次重试："+
							"上次错误=%q，sleep=%s（累计 sleep=%s），即将写入 sent=%d recv=%d",
							round, u.Hash, attempt, maxRetryAttempts,
							lastErr, waitThisTime, waitTotal, sent, recv)

						select {
						case <-time.After(waitThisTime):
						case <-a.ctx.Done():
							// ctx 取消，跳出；外层 for-select 会捕捉到并退出
							return false
						}
					}

					lastErr = a.pst.UpdateUserTraffic(u.Hash, sent, recv)
					if lastErr == nil {
						break
					}
					if attempt == 0 {
						// 首次失败：判断是否可重试
						if retryableError(lastErr) {
							retriedOnce = true
							log.Warnf("[batchTrafficUpdater] round=%d hash=%s 首次写库失败（可重试）："+
								"sent=%d (+%d), recv=%d (+%d), err=%q。将进行最多 %d 次指数退避重试",
								round, u.Hash, sent, deltaSent, recv, deltaRecv, lastErr, maxRetryAttempts)
						} else {
							// 不可重试错误：直接放弃
							log.Warnf("[batchTrafficUpdater] round=%d hash=%s 首次写库失败（不可重试）："+
								"sent=%d (+%d), recv=%d (+%d), err=%q。跳过重试，本轮放弃",
								round, u.Hash, sent, deltaSent, recv, deltaRecv, lastErr)
							break
						}
					} else {
						// 重试过程中的失败；如果已经不是可重试错误，提前跳出
						if !retryableError(lastErr) {
							log.Warnf("[batchTrafficUpdater] round=%d hash=%s 第 %d/%d 次重试返回不可重试错误，提前终止：%q",
								round, u.Hash, attempt, maxRetryAttempts, lastErr)
							break
						}
					}
				}

				if lastErr != nil {
					failedUsers++
					totalErrors++
					if retriedOnce {
						totalRetryHit++
						totalRetryAborts++
						retryHitUsers++
						retryAborted++
						log.Warnf("[batchTrafficUpdater] round=%d hash=%s 重试全部耗尽仍失败："+
							"尝试次数=%d，累计等待=%s，sent=%d recv=%d，最终错误=%q。"+
							"⚠️ 本轮内存 → DB 未写入该用户，下轮自动再次尝试（累积增量保留）",
							round, u.Hash, attempt, waitTotal, sent, recv, lastErr)
					}
					// 没有进入重试分支的首次失败，上面已经单独打过日志，这里不再重复
					return true
				}

				// ---- 成功 ----
				successUsers++
				totalWrites++
				if retriedOnce {
					totalRetryHit++
					retryHitUsers++
					log.Infof("[batchTrafficUpdater] round=%d hash=%s 重试后写库成功 ✅："+
						"尝试次数=%d（含重试 %d 次），累计等待=%s，sent=%d (+%d), recv=%d (+%d)",
						round, u.Hash, attempt+1, attempt, waitTotal,
						sent, deltaSent, recv, deltaRecv)
				}

				oldLastSent := u.persistedSent.Swap(sent)
				oldLastRecv := u.persistedRecv.Swap(recv)
				if oldLastSent != lastSent || oldLastRecv != lastRecv {
					unmatchedUsers++
					log.Debugf("[batchTrafficUpdater] round=%d lastSent/lastRecv 并发冲突："+
						"hash=%s，before(load)=(%d,%d)，before(swap)=(%d,%d)，written=(%d,%d)",
						round, u.Hash,
						lastSent, lastRecv,
						oldLastSent, oldLastRecv,
						sent, recv)
				}

				log.Debugf("[batchTrafficUpdater] round=%d 用户流量写库成功："+
					"hash=%s，sent=%d (+%d), recv=%d (+%d)",
					round, u.Hash, sent, deltaSent, recv, deltaRecv)
				return true
			})

			roundElapsed := time.Since(roundStart)
			// 当本轮触发重试时，用 Info 级做聚合提示（因为重试是重要的运行状态）
			if retryHitUsers > 0 {
				log.Infof("[batchTrafficUpdater] round=%d 锁重试情况：触发过重试的用户=%d，"+
					"重试耗尽后仍失败=%d（其余用户重试后恢复）。本轮总耗时=%s（interval=%s）",
					round, retryHitUsers, retryAborted,
					roundElapsed.Round(time.Microsecond), interval)
			}

			// 每轮汇总（Debug 级，默认不刷屏）
			log.Debugf("[batchTrafficUpdater] round=%d 完成："+
				"总用户=%d，有变化=%d，成功=%d，失败=%d，并发冲突=%d，"+
				"触发过重试的用户=%d（耗尽 %d）；"+
				"本轮累计：sent=%s recv=%s；耗时=%s（interval=%s）",
				round,
				totalUsers, changedUsers, successUsers, failedUsers, unmatchedUsers,
				retryHitUsers, retryAborted,
				common.HumanFriendlyTraffic(sentBytes), common.HumanFriendlyTraffic(recvBytes),
				roundElapsed.Round(time.Microsecond), interval)

			// 当本轮出现失败时，额外 Warn 聚合提示
			if failedUsers > 0 {
				log.Warnf("[batchTrafficUpdater] round=%d 出现 %d 个用户写库失败 "+
					"(触发重试的用户=%d，重试耗尽=%d，不可重试直接放弃=%d)，已成功 %d 个用户",
					round, failedUsers, retryHitUsers, retryAborted,
					failedUsers-retryAborted, /* 未重试直接失败 = 不可重试 */
					successUsers)
			}
		}
	}
}

func (u *User) GetSpeed() (uint64, uint64) {
	return u.sendSpeed.Load(), u.recvSpeed.Load()
}

type Authenticator struct {
	users  sync.Map
	pst    statistic.Persistencer
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (a *Authenticator) AuthUser(hash string) (bool, statistic.User) {
	if user, found := a.users.Load(hash); found {
		u := user.(*User)
		// 到期或额度用尽即拒绝认证。不在这里 DelUser：对 SQLite 后端，DelUser 会连数据库里的
		// 那一行一起删掉，而“到期”不等于“删除”，管理员续期还要用这行。
		if isExpired(u.GetExpire()) || u.isOverQuota() {
			return false, nil
		}
		return true, u
	}
	return false, nil
}

func (a *Authenticator) AuthUserWithPassword(password string) (bool, statistic.User) {
	var foundUser statistic.User
	found := false
	a.users.Range(func(k, v any) bool {
		user := v.(*User)
		if isExpired(user.GetExpire()) || user.isOverQuota() {
			return true
		}
		if common.CheckPasswordHash(password, user.Hash) {
			foundUser = user
			found = true
			return false
		}
		return true
	})
	return found, foundUser
}

func (a *Authenticator) SetKeyShare(hash string, pwd string) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	user := u.(*User)
	user.setPassword(pwd)
	if a.pst != nil {
		if err := a.pst.SaveUser(user); err != nil {
			// 内存已经改好、库里没落，重启就回落到旧值。必须把失败交给调用方
			// （gRPC 的 SetUsers 会回 Success:false，命令行退出码接实），
			// 不能再只打一行日志然后返回 nil —— 那等于对运维报告"改成功了"。
			return common.NewErrorf("failed to persist user %v", hash).Base(err)
		}
	}
	return nil
}

func (u *User) GetKeyShare() string {
	u.passLock.RLock()
	defer u.passLock.RUnlock()
	return u.password
}

// emptyPasswordHash 是空密码的 SHA224。它是公开常量（谁都能算出来），所以既不能
// 出现在配置文件里，也不能被 AddUser/持久化后端装载接受 —— 那等于现场开一个免密用户。
// gRPC API 本身没有鉴权，所以运行时这条路比配置文件更值得堵。
var emptyPasswordHash = common.SHA224String("")

func rejectEmptyPassword(hash string) error {
	if hash == "" || hash == emptyPasswordHash {
		return common.NewError("refusing to use an empty password: its SHA224 hash is a " +
			"public constant, which would open a password-free user")
	}
	return nil
}

func (a *Authenticator) AddUser(hash string) error {
	if err := rejectEmptyPassword(hash); err != nil {
		return err
	}
	if _, found := a.users.Load(hash); found {
		return common.NewError("hash " + hash + " is already exist")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	meter := &User{
		Hash:    hash,
		ipTable: make(map[string]*ipSlot),
		ctx:     ctx,
		cancel:  cancel,
	}
	meter.wg.Go(func() {
		meter.speedUpdater()
	})
	meter.wg.Go(func() {
		meter.ipCleaner()
	})
	// 必须用 LoadOrStore：先 Load 再 Store 之间有窗口，两个并发的 AddUser(同一 hash)
	// 都能通过存在性检查，各自起 speedUpdater + ipCleaner，然后后者把前者覆盖掉。
	// 被覆盖的那个 User 再没有任何引用者（DelUser 只会关 map 里那一个），
	// 它的 cancel 永远不会被调用 —— 每撞一次永久漏 2 个 goroutine + 2 个 ticker +
	// 1 个 ctx + 1 张 ipTable。触发面是真实存在的：API 的 SetUsers(Add) 与
	// mysql 后端的 syncUsers 都会对同一个 hash 调 AddUser。
	if _, loaded := a.users.LoadOrStore(hash, meter); loaded {
		// 抢输了：把刚起的两个后台协程收掉。先 cancel 再 Wait，Close 正是这一对。
		// 协程是在登记之前起的，所以这里不存在"被别人先关掉"的次序问题。
		_ = meter.Close() //gosec:disable -- 错误忽略：User.Close 恒返回 nil
		return common.NewError("hash " + hash + " is already exist")
	}
	// 注意：此处不再调用 a.pst.SaveUser(meter)。
	// 原实现会让 AddUser 立即 SaveUser（此时 password 仍为 ""），紧接着调用方
	// 通常会调用 SetKeyShare(hash, password) 再次 SaveUser，造成两次 DB 写入，
	// 且在两次写入之间崩溃会在 DB 中留下空密码行（重启后 GetKeyShare 返回空串）。
	// 持久化统一由 SetKeyShare / SetUserIPLimit / SetUserSpeedLimit / SetUserTraffic
	// 等带语义的方法负责——这些方法都会写入完整字段。
	return nil
}

func (a *Authenticator) DelUser(hash string) error {
	meter, found := a.users.Load(hash)
	if !found {
		return common.NewError("hash " + hash + " not found")
	}
	meter.(*User).Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	a.users.Delete(hash)
	if a.pst != nil {
		a.pst.DeleteUser(hash) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	return nil
}

func (a *Authenticator) ListUsers() []statistic.User {
	result := make([]statistic.User, 0)
	a.users.Range(func(k, v any) bool {
		result = append(result, v.(*User))
		return true
	})
	return result
}

func (a *Authenticator) Close() error {
	// 顺序至关重要:必须先停 updater 再关用户。
	// 若先关用户,User.Close 的 ResetTraffic 会把 Sent/Recv 清零,而仍在运行的
	// batchTrafficUpdater 会读到 sent < persistedSent,uint64 下溢把天文数字写进 DB;
	// 且 updater 每轮都会撞上这种脏数据。
	a.cancel()
	a.wg.Wait()
	// 关停前做一次最终 flush。batchTrafficUpdater 每 10s 才写一次库，而下面 User.Close
	// 的第一件事就是 ResetTraffic 把累计量清零 —— 不 flush 就必然丢掉最后一个周期内的
	// 流量（sqlite 持久化下是真实的数据丢失，不是"少记一点"）。
	// 位置要求：必须在 a.wg.Wait() 之后（updater 已停，不会与它并发写同一用户），
	// 又必须在 users.Range(Close) 之前（清零之后就读不到待写的量了）。
	if a.pst != nil {
		a.users.Range(func(_, v any) bool {
			u := v.(*User)
			sent, recv := u.GetTraffic()
			if sent == u.persistedSent.Load() && recv == u.persistedRecv.Load() {
				return true // 与批处理一致：无变化不写库
			}
			if err := a.pst.UpdateUserTraffic(u.Hash, sent, recv); err != nil {
				log.Error(common.NewError("failed to flush user traffic on close").Base(err))
			}
			return true
		})
	}
	a.users.Range(func(k, v any) bool {
		v.(*User).Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return true
	})
	// 关闭持久化后端,释放 DB 连接池/文件句柄。必须在 a.wg.Wait() 之后:
	// batchTrafficUpdater 仍会通过 a.pst 写库,提前关闭会让它拿到已关闭的句柄。
	if a.pst != nil {
		if err := a.pst.Close(); err != nil {
			log.Error(common.NewError("failed to close persistencer").Base(err))
		}
	}
	return nil
}

func (a *Authenticator) SetUserTraffic(hash string, sent, recv uint64) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	user := u.(*User)
	user.setTraffic(sent, recv)
	if a.pst != nil {
		if err := a.pst.SaveUser(user); err != nil {
			// 内存已经改好、库里没落，重启就回落到旧值。必须把失败交给调用方
			// （gRPC 的 SetUsers 会回 Success:false，命令行退出码接实），
			// 不能再只打一行日志然后返回 nil —— 那等于对运维报告"改成功了"。
			return common.NewErrorf("failed to persist user %v", hash).Base(err)
		}
	}
	return nil
}

func (a *Authenticator) SetUserSpeedLimit(hash string, send, recv int) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	user := u.(*User)
	user.SetSpeedLimit(send, recv)
	if a.pst != nil {
		if err := a.pst.SaveUser(user); err != nil {
			// 内存已经改好、库里没落，重启就回落到旧值。必须把失败交给调用方
			// （gRPC 的 SetUsers 会回 Success:false，命令行退出码接实），
			// 不能再只打一行日志然后返回 nil —— 那等于对运维报告"改成功了"。
			return common.NewErrorf("failed to persist user %v", hash).Base(err)
		}
	}
	return nil
}

func (a *Authenticator) SetUserIPLimit(hash string, limit int) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	user := u.(*User)
	user.setIPLimit(limit)
	if a.pst != nil {
		if err := a.pst.SaveUser(user); err != nil {
			// 内存已经改好、库里没落，重启就回落到旧值。必须把失败交给调用方
			// （gRPC 的 SetUsers 会回 Success:false，命令行退出码接实），
			// 不能再只打一行日志然后返回 nil —— 那等于对运维报告"改成功了"。
			return common.NewErrorf("failed to persist user %v", hash).Base(err)
		}
	}
	return nil
}

// SetUserExpire 把后端（MySQL users.expire）的到期时刻推进内存用户，让 AuthUser 当场拒绝，
// 而不是等下一轮同步把它 DelUser 掉。
// 刻意不写 persistencer：这两个值本来就是持久化后端每轮读出来的事实，回写会形成
// 「读出来再写回去」的往返，并且在同时配了 sqlite 与 mysql 的部署里把 MySQL 的值盖进
// SQLite 的表。
func (a *Authenticator) SetUserExpire(hash string, expire int64) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	u.(*User).setExpire(expire)
	return nil
}

// SetUserQuota 登记额度准入判定，语义同 SetUserExpire：usedBase 是这一轮同步从库里读到的
// download+upload，内存里尚未刷库的增量由 isOverQuota 自己加上（见 setQuota）。
func (a *Authenticator) SetUserQuota(hash string, limit int64, usedBase uint64) error {
	u, exist := a.users.Load(hash)
	if !exist {
		return common.NewErrorf("user %v not found", hash)
	}
	u.(*User).setQuota(limit, usedBase)
	return nil
}

func NewAuthenticator(ctx context.Context) (statistic.Authenticator, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	// 空密码必须在这里、在任何资源分配之前就拒掉：SHA224("") 是一个公开常量，
	// 把它当成用户注册进去等于给全网开一条免密通道（模板渲染出空变量、
	// password: ["", "real"] 这类笔误都是真实场景）。
	// easy 模式（-password）本来就 log.Fatal 拒绝空密码，配置文件这条路不能更宽松。
	// 放在最上面是因为再往下就已经打开 sqlite 持久化器、起后台协程了。
	for i, password := range cfg.Passwords {
		if password == "" {
			return nil, common.NewError(fmt.Sprintf(
				"password #%d is empty: the hash of an empty password is a public constant, refusing to start", i+1))
		}
	}
	// 派生自有 ctx:认证器的生命周期(含 batchTrafficUpdater)由自身 Close 控制,
	// 不依赖调用方取消父 ctx
	ctx, cancel := context.WithCancel(ctx)
	a := &Authenticator{
		ctx:    ctx,
		cancel: cancel,
	}
	var err error
	if cfg.Sqlite != "" {
		a.pst, err = sqlite.NewSqlitePersistencer(cfg.Sqlite)
		if err != nil {
			cancel()
			return nil, common.NewError(`failed to open the sqlite persistencer (check the "sqlite" config field)`).Base(err)
		}
	}
	if a.pst != nil {
		err := a.pst.ListUser(func(hash string, u statistic.Metadata) bool {
			if hash == "" || hash == emptyPasswordHash {
				// 空密码行可能来自老版本：AddUser 与 SetKeyShare 之间崩溃就会留下
				// 一条密码为空的用户。装载它等于恢复那条免密通道，所以只报错、不装载；
				// 清理要直接操作数据库文件（这里不能走 DelUser，用户根本没进内存表）。
				log.Error("跳过持久化后端里的空密码用户 hash=" + hash +
					"：空密码的 SHA224 是公开常量，等于免密通道；请删除该条数据库记录")
				return true
			}
			if _, found := a.users.Load(hash); found {
				log.Error("hash " + hash + " is already exist")
				return true
			}
			ctx, cancel := context.WithCancel(a.ctx)
			user := &User{
				Hash:    hash,
				ipTable: make(map[string]*ipSlot),
				ctx:     ctx,
				cancel:  cancel,
			}
			user.setIPLimit(u.GetIPLimit())
			user.SetSpeedLimit(u.GetSpeedLimit())
			user.setTraffic(u.GetTraffic())
			user.setPassword(u.GetKeyShare())
			user.setExpire(u.GetExpire())
			user.wg.Go(func() {
				user.speedUpdater()
			})
			user.wg.Go(func() {
				user.ipCleaner()
			})
			a.users.Store(hash, user)
			return true
		})
		if err != nil {
			log.Errorf("List user from persistencer: %s", err)
		}
	}
	for _, password := range cfg.Passwords {
		// trojan 协议要求客户端发送 hex(SHA224(password))，必须用确定性哈希，
		// 不能用 bcrypt（随机 salt 导致双端结果不同，认证永远失败）
		hash := common.SHA224String(password)
		a.AddUser(hash)                          //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		a.SetKeyShare(hash, password)            //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		a.SetUserIPLimit(hash, cfg.MaxIPPerUser) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	if a.pst != nil {
		pstType := fmt.Sprintf("%T", a.pst)
		log.Infof("[memory-authenticator] 已启用持久化后端：%s，将启动 batchTrafficUpdater（每 10s 统一写库）", pstType)
		// 用 wg.Go 而非裸 go:Close() 的 wg.Wait() 必须能等到 updater 退出,
		// 否则 cancel 后它可能还在写库
		a.wg.Go(a.batchTrafficUpdater)
	} else {
		log.Info("[memory-authenticator] 未配置持久化后端（sqlite/mysql），用户流量仅在内存中保存，进程退出后丢失")
	}
	log.Debug("memory authenticator created")
	return a, nil
}

func init() {
	statistic.RegisterAuthenticatorCreator(Name, NewAuthenticator)
}
