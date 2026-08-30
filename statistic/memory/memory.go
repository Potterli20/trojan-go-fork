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

type User struct {
	Sent          uint64
	Recv          uint64
	lastSent      uint64
	lastRecv      uint64
	sendSpeed     atomic.Uint64
	recvSpeed     atomic.Uint64
	persistedSent atomic.Uint64 // 持久化基线，仅 batchTrafficUpdater 读写，与 speedUpdater 使用的 lastSent 物理分离
	persistedRecv atomic.Uint64 // 持久化基线，仅 batchTrafficUpdater 读写，与 lastRecv 物理分离
	Hash          string
	password      string
	passLock      sync.RWMutex
	ipTable       map[string]time.Time
	ipNum         atomic.Int32
	MaxIPNum      int
	limiterLock   sync.RWMutex
	ipLock        sync.Mutex
	SendLimiter   *rate.Limiter
	RecvLimiter   *rate.Limiter
	ctx           context.Context
	wg            sync.WaitGroup
	cancel        context.CancelFunc
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
		case <-ticker.C:
			u.ipLock.Lock()
			now := time.Now()
			for ip, lastSeen := range u.ipTable {
				if now.Sub(lastSeen) >= 10*time.Second {
					delete(u.ipTable, ip)
					u.ipNum.Add(-1)
				}
			}
			u.ipLock.Unlock()
		}
	}
}

func (u *User) AddIP(ip string) bool {
	if u.MaxIPNum <= 0 {
		return true
	}

	u.ipLock.Lock()
	defer u.ipLock.Unlock()

	if _, found := u.ipTable[ip]; found {
		u.ipTable[ip] = time.Now()
		return true
	}

	if int(u.ipNum.Load()) >= u.MaxIPNum {
		return false
	}

	u.ipTable[ip] = time.Now()
	u.ipNum.Add(1)
	return true
}

func (u *User) DelIP(ip string) bool {
	if u.MaxIPNum <= 0 {
		return true
	}

	u.ipLock.Lock()
	defer u.ipLock.Unlock()

	if _, found := u.ipTable[ip]; !found {
		return false
	}

	delete(u.ipTable, ip)
	u.ipNum.Add(-1)

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
	defer u.passLock.Unlock()
	u.password = pwd
}

func (u *User) GetIPLimit() int {
	u.ipLock.Lock()
	defer u.ipLock.Unlock()
	return u.MaxIPNum
}

func (u *User) AddSentTraffic(sent int) {
	u.limiterLock.RLock()
	limiter := u.SendLimiter
	u.limiterLock.RUnlock()
	// WaitN 可能长时间阻塞（限速生效期间），不能持有 limiterLock 等待，
	// 否则 SetSpeedLimit 的写锁会长期饥饿，运行中修改限速迟迟不生效。
	if limiter != nil && sent >= 0 {
		limiter.WaitN(u.ctx, sent)
	}
	atomic.AddUint64(&u.Sent, uint64(sent))
}

func (u *User) AddRecvTraffic(recv int) {
	u.limiterLock.RLock()
	limiter := u.RecvLimiter
	u.limiterLock.RUnlock()
	if limiter != nil && recv >= 0 {
		limiter.WaitN(u.ctx, recv)
	}
	atomic.AddUint64(&u.Recv, uint64(recv))
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
	atomic.StoreUint64(&u.Sent, send)
	atomic.StoreUint64(&u.Recv, recv)
}

func (u *User) GetTraffic() (uint64, uint64) {
	return atomic.LoadUint64(&u.Sent), atomic.LoadUint64(&u.Recv)
}

func (u *User) ResetTraffic() (uint64, uint64) {
	sent := atomic.SwapUint64(&u.Sent, 0)
	recv := atomic.SwapUint64(&u.Recv, 0)
	atomic.StoreUint64(&u.lastSent, 0)
	atomic.StoreUint64(&u.lastRecv, 0)
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
			u.sendSpeed.Store(sent - atomic.SwapUint64(&u.lastSent, sent))
			u.recvSpeed.Store(recv - atomic.SwapUint64(&u.lastRecv, recv))
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
		return true, user.(*User)
	}
	return false, nil
}

func (a *Authenticator) AuthUserWithPassword(password string) (bool, statistic.User) {
	var foundUser statistic.User
	found := false
	a.users.Range(func(k, v any) bool {
		user := v.(*User)
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
		err := a.pst.SaveUser(user)
		if err != nil {
			log.Errorf("Save user %s failed: %s", hash, err)
		}
	}
	return nil
}

func (u *User) GetKeyShare() string {
	u.passLock.RLock()
	defer u.passLock.RUnlock()
	return u.password
}

func (a *Authenticator) AddUser(hash string) error {
	if _, found := a.users.Load(hash); found {
		return common.NewError("hash " + hash + " is already exist")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	meter := &User{
		Hash:    hash,
		ipTable: make(map[string]time.Time),
		ctx:     ctx,
		cancel:  cancel,
	}
	meter.wg.Go(func() {
		meter.speedUpdater()
	})
	meter.wg.Go(func() {
		meter.ipCleaner()
	})
	a.users.Store(hash, meter)
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
	meter.(*User).Close()
	a.users.Delete(hash)
	if a.pst != nil {
		a.pst.DeleteUser(hash)
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
	a.users.Range(func(k, v any) bool {
		v.(*User).Close()
		return true
	})
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
		err := a.pst.SaveUser(user)
		if err != nil {
			log.Errorf("Save user %s failed: %s", hash, err)
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
		err := a.pst.SaveUser(user)
		if err != nil {
			log.Errorf("Save user %s failed: %s", hash, err)
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
		err := a.pst.SaveUser(user)
		if err != nil {
			log.Errorf("Save user %s failed: %s", hash, err)
		}
	}
	return nil
}

func NewAuthenticator(ctx context.Context) (statistic.Authenticator, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
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
			return nil, err
		}
	}
	if a.pst != nil {
		err := a.pst.ListUser(func(hash string, u statistic.Metadata) bool {
			if _, found := a.users.Load(hash); found {
				log.Error("hash " + hash + " is already exist")
				return true
			}
			ctx, cancel := context.WithCancel(a.ctx)
			user := &User{
				Hash:    hash,
				ipTable: make(map[string]time.Time),
				ctx:     ctx,
				cancel:  cancel,
			}
			user.setIPLimit(u.GetIPLimit())
			user.SetSpeedLimit(u.GetSpeedLimit())
			user.setTraffic(u.GetTraffic())
			user.setPassword(u.GetKeyShare())
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
		a.AddUser(hash)
		a.SetKeyShare(hash, password)
		a.SetUserIPLimit(hash, cfg.MaxIPPerUser)
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
