package mysql

// 本包此前一份测试都没有，而它的每一条判定分支（建表补列、到期、超额、写回失败回滚、
// 关停顺序）都躲在真实 MySQL 之后，CI 与本机构不出一个 MySQL 服务。
// mysql.go 里的 mysqlDB 接口就是为这些用例开的最小注入点：真实路径仍然走 *sql.DB，
// 方法签名逐字相同。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

// fakeRow 是 users 表里的一行，字段与 SELECT 的 7 列一一对应。
type fakeRow struct {
	username, hash       string
	quota, download, upl int64
	maxip                int
	expire               int64
}

type fakeCall struct {
	query string
	args  []any
}

type fakeResult struct{ affected int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.affected, nil }

type fakeDB struct {
	mu    sync.Mutex
	calls []fakeCall
	rows  []fakeRow

	// execErr/queryErr 按语句文本决定失败与否。nil 表示一律成功。
	execErr  func(query string) error
	queryErr func(query string) error
	// queryHook 在 QueryContext 返回结果集之前执行，用来把动作精确插到
	// 「本轮写回之后、判定之前」那一段窗口里。
	queryHook func(query string)
}

func (f *fakeDB) record(q string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{query: q, args: args})
}

// callsWith 返回语句文本包含 sub 的那些调用，按发生顺序。
func (f *fakeDB) callsWith(sub string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if strings.Contains(c.query, sub) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.record(query, args...)
	if f.execErr != nil {
		if err := f.execErr(query); err != nil {
			return nil, err
		}
	}
	return fakeResult{affected: 1}, nil
}

func (f *fakeDB) QueryContext(_ context.Context, query string, _ ...any) (rowSet, error) {
	f.record(query)
	if f.queryErr != nil {
		if err := f.queryErr(query); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	rows := append([]fakeRow(nil), f.rows...)
	f.mu.Unlock()
	if f.queryHook != nil {
		f.queryHook(query)
	}
	return &fakeRows{pending: -1, rows: rows, query: query}, nil
}

func (f *fakeDB) Close() error { return nil }

type fakeRows struct {
	rows    []fakeRow
	cursor  int
	pending int
	query   string
	scanErr error
}

func (r *fakeRows) Next() bool {
	if r.scanErr != nil {
		return false
	}
	if r.cursor >= len(r.rows) {
		r.pending = -1
		return false
	}
	r.pending = r.cursor
	r.cursor++
	// 列探测语句（SELECT `maxip` FROM `users` LIMIT 0）从不被迭代，走到这里就是
	// "没有数据行"；users 查询才需要真的吐出行来。
	if strings.Contains(r.query, "LIMIT 0") {
		r.pending = -1
		return false
	}
	return true
}

// Scan 按 syncUsers 里那 7 个目标指针的顺序赋值。类型断言失败会立刻报错，
// 所以这条也顺带钉住了「SELECT 列表顺序」与「Scan 顺序」必须一致。
func (r *fakeRows) Scan(dest ...any) error {
	if r.pending < 0 {
		return errors.New("fakeRows.Scan 在没有当前行时被调用")
	}
	row := r.rows[r.pending]
	want := []string{"*string", "*string", "*int64", "*int64", "*int64", "*int", "*int64"}
	if len(dest) != len(want) {
		return fmt.Errorf("Scan 目标个数是 %d，期望 %d（SELECT 的列数变了）", len(dest), len(want))
	}
	for i, d := range dest {
		if got := fmt.Sprintf("%T", d); got != want[i] {
			return fmt.Errorf("Scan 第 %d 个目标类型是 %s，期望 %s：SELECT 与 Scan 的顺序必须逐字对齐", i+1, got, want[i])
		}
	}
	*dest[0].(*string) = row.username
	*dest[1].(*string) = row.hash
	*dest[2].(*int64) = row.quota
	*dest[3].(*int64) = row.download
	*dest[4].(*int64) = row.upl
	*dest[5].(*int) = row.maxip
	*dest[6].(*int64) = row.expire
	return nil
}

func (r *fakeRows) Err() error   { return r.scanErr }
func (r *fakeRows) Close() error { return nil }

func newFakeAuthenticator(t *testing.T, db mysqlDB) *Authenticator {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mem, err := memory.NewAuthenticator(config.WithConfig(ctx, memory.Name, &memory.Config{}))
	if err != nil {
		t.Fatalf("memory.NewAuthenticator: %v", err)
	}
	return &Authenticator{
		Authenticator: mem.(*memory.Authenticator),
		db:            db,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func TestSyncUsersPropagatesQuotaIncludingUnflushedTraffic(t *testing.T) {
	// 库侧已用 900（download 600 + upload 300），额度 1000：本轮同步后用户仍在内存，
	// 认证放行。关键是**内存里尚未刷库的那段增量也要计入**——旧实现只看库里的数字，
	// 于是用户在这一个 check_rate 周期内可以继续超额。
	const hash = "u-quota"
	db := &fakeDB{rows: []fakeRow{{
		username: hash, hash: hash, quota: 1000, download: 600, upl: 300,
	}}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()

	valid, user := a.AuthUser(hash)
	if !valid || user == nil {
		t.Fatal("库侧 900/1000，用户应当可用；同步却没把它装进内存")
	}
	user.AddSentTraffic(50) // 950 < 1000
	if ok, _ := a.AuthUser(hash); !ok {
		t.Fatal("额度还没用尽就被拒绝了：额度判定算错了基线")
	}
	user.AddSentTraffic(60) // 900 + 110 = 1010 > 1000

	// 判据：**不再跑第二轮同步**，AuthUser 必须当场拒绝。
	if ok, _ := a.AuthUser(hash); ok {
		t.Fatal("额度已用尽（库侧 900 + 内存未刷 110），AuthUser 仍放行：" +
			"说明 syncUsers 没把 quota/基线推进内存用户，超额窗口要等下一轮才生效")
	}
}

func TestSyncUsersUnlimitedAndZeroQuotaSemantics(t *testing.T) {
	// quota < 0 = 不限流量；quota = 0 = 无额度。这两个边界必须与库里现有语义逐字一致，
	// 尤其不能让"内存用户没有额度概念"退化成 quota=0（那会把所有人拒掉）。
	const unlimited, zero = "u-unlimited", "u-zero"
	db := &fakeDB{rows: []fakeRow{
		{username: unlimited, hash: unlimited, quota: -1, download: 1 << 40},
		{username: zero, hash: zero, quota: 0},
	}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()

	if ok, _ := a.AuthUser(unlimited); !ok {
		t.Fatal("quota<0 应当不限流量")
	}
	user := mustUser(t, a, unlimited)
	user.AddSentTraffic(1 << 20)
	if ok, _ := a.AuthUser(unlimited); !ok {
		t.Fatal("quota<0 的用户再加流量后仍应放行：不限流量的判定被 quotaChecked 关掉才是对的")
	}
	if ok, _ := a.AuthUser(zero); ok {
		t.Fatal("quota=0 是无额度，syncUsers 应当把它从内存删掉")
	}
	for _, u := range a.ListUsers() {
		if u.GetHash() == zero {
			t.Fatal("quota=0 的用户仍在内存里")
		}
	}
}

func TestSyncUsersPropagatesExpireToAuthTime(t *testing.T) {
	// 这一条直接回答 issue #102 的"expire 这个还支持吗"。
	// 旧实现只在每轮同步里把到期用户 DelUser，于是刚过期的用户在下一轮之前（最长一个
	// check_rate）仍然认证通过。修好后到期时刻随同步进入内存用户，跨过那一刻即拒绝，
	// **不需要再跑一轮同步**。
	const hash = "u-expire"
	deadline := time.Now().Unix() + 2
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: -1, expire: deadline}}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()
	if ok, _ := a.AuthUser(hash); !ok {
		t.Fatalf("expire 在 %d（未来）时不该拒绝认证", deadline)
	}
	user := mustUser(t, a, hash)
	if got := user.GetExpire(); got != deadline {
		t.Fatalf("内存用户带的到期时刻是 %d，期望 %d：syncUsers 没有把 users.expire 推进内存，"+
			"到期只能等下一轮同步的 DelUser", got, deadline)
	}

	// 有界轮询跨过那道时限，然后**立刻**要求拒绝——这一步把"何时到期"从假设变成读数。
	waitUntil(t, deadline)
	if ok, _ := a.AuthUser(hash); ok {
		t.Fatal("已跨过 expire 却仍放行：AuthUser 没有做到期判定")
	}
}

func TestSyncUsersDropsAlreadyExpiredRow(t *testing.T) {
	const hash = "u-expired"
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: -1, expire: time.Now().Unix() - 1}}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()

	if ok, _ := a.AuthUser(hash); ok {
		t.Fatal("库里已过期的行不应留在内存")
	}
	if len(a.ListUsers()) != 0 {
		t.Fatalf("期望内存里没有用户，实际 %d 个", len(a.ListUsers()))
	}
}

func TestSyncUsersNeverExpiresDefault(t *testing.T) {
	// expire = 0（列默认值）必须是永不过期：这条是整个到期改动的地基，写反了就会把
	// 所有没设过 expire 的存量用户全部拒掉。
	const hash = "u-zero-expire"
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: -1, expire: 0}}}
	a := newFakeAuthenticator(t, db)

	a.syncUsers()
	waitUntil(t, time.Now().Unix()+1) // 跨过一秒，确认"0"不会被读成"1970 年就到期"
	if ok, _ := a.AuthUser(hash); !ok {
		t.Fatal("expire=0 被当成已到期了：默认值语义写反")
	}
}

func TestSyncUserFlushesTrafficBeforeDeletingUser(t *testing.T) {
	// DelUser 会调 User.Close()，而 Close 里的 ResetTraffic 只把计数器**清零**、不写库。
	// 本轮开头的 writeBackTraffic 已经把那时的增量写走了，此后又进来的那段流量若不在删除前
	// 补一次写回，就随那个 User 对象一起永久消失。
	const hash = "u-flush-on-delete"
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: 1000}}}
	a := newFakeAuthenticator(t, db)
	a.syncUsers() // 用户进内存：quota=1000，基线 0
	user := mustUser(t, a, hash)

	// 下一轮库里显示已经用满（download=1000），而"本轮写回之后、判定之前"又进来了 777。
	// queryHook 就是这个插入点：它跑在 syncUsers 拿到结果集之前，也就是 writeBackTraffic 之后。
	db.rows = []fakeRow{{username: hash, hash: hash, quota: 1000, download: 1000}}
	db.queryHook = func(q string) {
		if strings.Contains(q, "FROM users") {
			user.AddSentTraffic(777)
		}
	}
	a.syncUsers()

	var flushed uint64
	for _, c := range db.callsWith("UPDATE") {
		// 参数顺序与 updateTrafficQuery 一致：recv, sent, hash
		if len(c.args) == 3 && c.args[2].(string) == hash {
			if sent, ok := c.args[1].(uint64); ok {
				flushed += sent
			}
		}
	}
	if flushed < 777 {
		t.Fatalf("删除用户前那段 777 字节的增量没写回库（累计 sent 只看到 %d）："+
			"DelUser 会 Close，Close 里的 ResetTraffic 直接把它清零丢弃", flushed)
	}
	if valid, _ := a.AuthUser(hash); valid {
		t.Fatal("额度已用尽的用户应当被移出内存")
	}
}

func TestSyncUsersWriteBackFailureRestoresTraffic(t *testing.T) {
	// 写库失败时，已经从内存取走的增量必须原样加回（RestoreTraffic 不过限速器），
	// 否则这段统计本轮被丢弃、下一轮从 0 重新累计，永远补不回来。
	const hash = "u-restore"
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: -1}}}
	a := newFakeAuthenticator(t, db)
	a.syncUsers()

	user := mustUser(t, a, hash)
	user.AddSentTraffic(4096)

	db.execErr = func(q string) error {
		if strings.HasPrefix(q, "UPDATE") {
			return errors.New("Error 1213: Deadlock found when trying to get lock")
		}
		return nil
	}
	a.syncUsers()

	sent, recv := user.GetTraffic()
	if sent != 4096 || recv != 0 {
		t.Fatalf("写库失败后增量应原样留在内存，实际 sent=%d recv=%d（被清零且没补回）", sent, recv)
	}
	if valid, _ := a.AuthUser(hash); !valid {
		t.Fatal("一次写库失败不该把用户从内存删掉")
	}
}

func TestSyncUsersAbandonsRoundWhenQueryFails(t *testing.T) {
	// 查询中途失败时整轮放弃：userMap 不完整，据此清理会把合法用户误删。
	const hash = "u-keep"
	db := &fakeDB{rows: []fakeRow{{username: hash, hash: hash, quota: -1}}}
	a := newFakeAuthenticator(t, db)
	a.syncUsers()
	if valid, _ := a.AuthUser(hash); !valid {
		t.Fatal("前置条件：用户应已在内存")
	}

	db.queryErr = func(q string) error {
		if strings.Contains(q, "FROM users") {
			return errors.New("Error 2013: lost connection to server")
		}
		return nil
	}
	a.syncUsers()

	if valid, _ := a.AuthUser(hash); !valid {
		t.Fatal("MySQL 抖动一轮就把合法用户删掉了：本轮必须整段放弃")
	}
}

func TestEnsureUsersTableCreatesTableAndAddsMissingColumns(t *testing.T) {
	db := &fakeDB{}
	if err := ensureUsersTable(context.Background(), db); err != nil {
		t.Fatalf("ensureUsersTable: %v", err)
	}
	for _, want := range []string{"CREATE TABLE IF NOT EXISTS", "ADD COLUMN `maxip`", "ADD COLUMN `expire`"} {
		if len(db.callsWith(want)) != 1 {
			t.Errorf("缺少一次 %q 的语句执行（实际调用：%s）", want, dumpCalls(db))
		}
	}
}

func TestEnsureUsersTableToleratesColumnsThatAlreadyExist(t *testing.T) {
	// 旧表已经有这两列时 ALTER 会报 "Duplicate column name"，必须靠列探测放行。
	db := &fakeDB{execErr: func(q string) error {
		if strings.Contains(q, "ADD COLUMN") {
			return errors.New("Error 1060: Duplicate column name")
		}
		return nil
	}}
	if err := ensureUsersTable(context.Background(), db); err != nil {
		t.Fatalf("列已存在时应当放行，却报错：%v", err)
	}
}

func TestEnsureUsersTableToleratesMissingDDLPrivilege(t *testing.T) {
	// 表已存在但账号没有 DDL 权限：CREATE 失败而关键列齐全 ⇒ 只告警、继续运行。
	db := &fakeDB{execErr: func(q string) error {
		if strings.Contains(q, "CREATE TABLE") {
			return errors.New("Error 1142: CREATE command denied to user")
		}
		return nil
	}}
	if err := ensureUsersTable(context.Background(), db); err != nil {
		t.Fatalf("只有 DML 权限时应当继续运行，却报错：%v", err)
	}
}

func TestEnsureUsersTableFailsWhenColumnCannotBeAdded(t *testing.T) {
	// ALTER 与列探测都失败 ⇒ 必须启动即失败并给出可执行的文案，
	// 否则整轮同步会每轮静默失败（表现为"流量不回写、用户增减不同步"却不报致命错）。
	db := &fakeDB{
		execErr: func(q string) error {
			if strings.Contains(q, "ADD COLUMN") {
				return errors.New("Error 1142: ALTER command denied to user")
			}
			return nil
		},
		queryErr: func(q string) error {
			if strings.Contains(q, "LIMIT 0") {
				return errors.New("Error 1054: Unknown column")
			}
			return nil
		},
	}
	err := ensureUsersTable(context.Background(), db)
	if err == nil {
		t.Fatal("补不上列时必须返回错误")
	}
	if !strings.Contains(err.Error(), "ALTER TABLE") {
		t.Fatalf("错误文案要给出可执行的补救动作，实际：%v", err)
	}
}

// --- helpers ---

func mustUser(t *testing.T, a *Authenticator, hash string) statistic.User {
	t.Helper()
	// AuthUser 现在会因为到期/超额拒绝返回对象，所以取用户用 ListUsers 快照。
	for _, u := range a.ListUsers() {
		if u.GetHash() == hash {
			return u
		}
	}
	t.Fatalf("内存里没有用户 %s（ListUsers 只有 %d 个）", hash, len(a.ListUsers()))
	return nil
}

// waitUntil 有界地等到 Unix 秒越过 deadline。它不是"睡一会儿赌它好了"：判据要求在跨过
// 那道时限之后**立刻**成立，所以这里只负责把时钟推过界，不承担"应当已经生效"的推断。
func waitUntil(t *testing.T, deadline int64) {
	t.Helper()
	deadlineAt := time.Unix(deadline, 0)
	if !time.Now().Before(deadlineAt) {
		return
	}
	select {
	case <-time.After(time.Until(deadlineAt)):
	case <-time.After(10 * time.Second):
		t.Fatalf("等了 10s 仍没跨过 %v：用例的时限不可用", deadlineAt)
	}
}

func dumpCalls(f *fakeDB) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sb strings.Builder
	for _, c := range f.calls {
		sb.WriteString("[" + c.query + "] ")
	}
	return sb.String()
}

// 编译期确认真实的 *sql.DB 经过 sqlBackend 满足注入接口。
var _ mysqlDB = sqlBackend{}
