package memory

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

// sqliteRefreshInterval 是「面板/SQL 直接 UPDATE 了 sqlite 里的 expire / maxip / 限速列」
// 被采纳进内存用户的间隔，存纳秒。用原子而不是裸变量：判据要在运行期把它调小，而这条轮
// 跑在自己的协程里（口径同 tunnel/simplesocks 的 headerTimeout）。<=0 取默认值。
var sqliteRefreshInterval atomic.Int64

const sqliteRefreshIntervalDefault = time.Minute

func sqliteRefreshEvery() time.Duration {
	if d := time.Duration(sqliteRefreshInterval.Load()); d > 0 {
		return d
	}
	return sqliteRefreshIntervalDefault
}

// noPersistencerRefreshKey 由 MySQL 后端在调 NewAuthenticator 之前注入。
//
// 为什么需要这道闸：statistic/mysql 的 Authenticator 内嵌一个 *memory.Authenticator，用的
// 是同一份配置上下文——也就是说 `mysql` 与 `memory.sqlite` 是可以同时生效的。那两个后端各自
// 都会把 expire / maxip / 限速推进同一批内存用户，再起一条 SQLite 刷新轮就是互相盖值：
// MySQL 里已经到期的用户会被 SQLite 那条（由 AddUser 顺手写进去的、expire=0 的）镜像行
// 复活成永不过期。MySQL 每轮 syncUsers 本来就在推这三列，所以这一侧直接不启动刷新轮。
type noPersistencerRefreshKey struct{}

// WithoutPersistencerRefresh 返回一个禁止 memory 认证器自起持久化刷新轮的 ctx。
// 只给「另一个后端已经在每轮推送同一批列」的调用方用（现在是 statistic/mysql）。
func WithoutPersistencerRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, noPersistencerRefreshKey{}, true)
}

func persistencerRefreshSuppressed(ctx context.Context) bool {
	suppressed, _ := ctx.Value(noPersistencerRefreshKey{}).(bool)
	return suppressed
}

// limitsRefresher 周期性把持久化后端里已有的用户的 expire / IP 上限 / 速度上限重新推进
// 内存用户。只在配了持久化后端、且没被上层禁用时起（与 batchTrafficUpdater 同一个门禁）。
//
// 修的是「接受但不生效」：SQLite 后端原先只在 NewAuthenticator 装载时读一次这些列，之后
// 面板直接改库里的 expire，**到期**与**续期**两条都要等重启才生效。按本项目口径
// （AGENTS.md 2026-10-27）配置被接受而实际不起作用算缺陷，不是待办。
func (a *Authenticator) limitsRefresher() {
	for {
		// 每轮重新取间隔：判据在运行期改那个原子值，用 time.Ticker 就固定死了。
		timer := time.NewTimer(sqliteRefreshEvery())
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := a.pst.ListUser(a.applyPersistedLimits); err != nil {
			log.Error(common.NewError("failed to refresh user limits from the persistencer").Base(err))
		}
	}
}

// applyPersistedLimits 是 ListUser 的回调：一行一次，全表一轮，而不是逐用户 LoadUser
// （那等于每轮 N 次查询）。回调恒返回 true 把表走完。
//
// 三条刻意的边界：
//   - **不新增用户**：库里多出来的行不当用户加回去，否则 DelUser 掉的人下一轮就被捞活。
//   - **不删除用户**：到期不等于删除（续期还要用这行），拒绝由 AuthUser 的 isExpired 当场做。
//     表里没有对应内存用户时（missing 那条出口）也只计数、不动手。
//   - **绝不碰 quota/quotaChecked**：SQLite 的表结构里没有额度列，把 quotaChecked 打开就等于
//     给所有 SQLite 用户设了 quota=0（无额度）——那正是 AGENTS.md 里「不能拿零值当没有额度
//     概念」踩过的坑。额度仍然只有 MySQL 有。
//
// 三个 setter 都走用户对象上的内部方法而不是 Authenticator 的 SetUserIPLimit /
// SetUserSpeedLimit：那两个是「运维改值」的入口，会 SaveUser 写回库里，而这里是在
// 读库里已有的事实，回写就是每轮把整行重抄一遍（还会与 batchTrafficUpdater 抢流量列）。
func (a *Authenticator) applyPersistedLimits(hash string, u statistic.Metadata) bool {
	v, found := a.users.Load(hash)
	if !found {
		return true
	}
	user := v.(*User)
	if expire := u.GetExpire(); expire != user.GetExpire() {
		user.setExpire(expire)
	}
	if limit := u.GetIPLimit(); limit != user.GetIPLimit() {
		user.setIPLimit(limit)
	}
	send, recv := u.GetSpeedLimit()
	if curSend, curRecv := user.GetSpeedLimit(); curSend != send || curRecv != recv {
		// SetSpeedLimit 会重建两个 rate.Limiter，只在值真的变了时做：
		// 每分钟无谓重建会把已积累的令牌丢掉，等效于给刚跑完一段的用户重新给满桶。
		user.SetSpeedLimit(send, recv)
	}
	return true
}
