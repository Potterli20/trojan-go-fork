package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	// MySQL Driver
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

const Name = "MYSQL"

// usersTableDDL 是 users 表的期望结构。旧版本（stat/db.go 的 TrafficMeter 时代）会在
// 启动时自动建表，后来重构把这段删掉了，于是沿用旧表的部署在缺列（maxip / expire）时
// 同步静默失效。这里恢复幂等建表，并对已存在的旧表补列（见 ensureUsersTable）。
const usersTableDDL = "CREATE TABLE IF NOT EXISTS `users` (" +
	"`username` VARCHAR(64) NOT NULL," +
	"`password` CHAR(56) NOT NULL," +
	"`quota` BIGINT NOT NULL DEFAULT 0," +
	"`download` BIGINT UNSIGNED NOT NULL DEFAULT 0," +
	"`upload` BIGINT UNSIGNED NOT NULL DEFAULT 0," +
	"`maxip` INT NOT NULL DEFAULT 0," +
	"`expire` BIGINT NOT NULL DEFAULT 0," +
	"PRIMARY KEY (`password`)" +
	")"

// usersTableExtraColumns 是升级过程中可能从旧表缺失的列。语句对“列已存在”是幂等的
// 兜底：先执行 ALTER，失败时用一次探测区分“已经加过了”和“真的加不上”。
var usersTableExtraColumns = []struct{ name, ddl string }{
	{"maxip", "ALTER TABLE `users` ADD COLUMN `maxip` INT NOT NULL DEFAULT 0"},
	{"expire", "ALTER TABLE `users` ADD COLUMN `expire` BIGINT NOT NULL DEFAULT 0"},
}

// ensureUsersTable 在认证器启动前把 users 表准备到可用状态：缺表则建，缺列则补。
// 注意日志里不要出现敏感关键词（password 等），否则整行会被脱敏成 [REDACTED]。
func ensureUsersTable(ctx context.Context, db mysqlDB) error {
	_, createErr := db.ExecContext(ctx, usersTableDDL)
	for _, col := range usersTableExtraColumns {
		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			if probeErr := probeUsersColumn(ctx, db, col.name); probeErr != nil {
				return common.NewError("failed to add required column `" + col.name + "` to users table; run the ALTER TABLE manually").Base(err)
			}
		}
	}
	if createErr != nil {
		// 建表语句失败但关键列齐全：多半是表已存在、而当前账号只有 DML 权限。
		// 这种情况放行，但明确告警，不要让它再次变成静默问题。
		log.Warn(common.NewError("cannot create users table (likely missing DDL privilege); required columns are present, continuing").Base(createErr))
	}
	return nil
}

// probeUsersColumn 探测 users 表是否已有某列（列名只来自上面的常量，非外部输入）。
func probeUsersColumn(ctx context.Context, db mysqlDB, name string) error {
	rows, err := db.QueryContext(ctx, "SELECT `"+name+"` FROM `users` LIMIT 0") //gosec:disable -- name 只来自包内常量，非外部输入
	if err != nil {
		return err
	}
	return rows.Close()
}

// rowSet 是 *sql.Rows 在同步逻辑里真正用到的那部分。*sql.Rows 的方法集与它逐字相符，
// 所以真实驱动直接满足这个接口，不需要适配层。
type rowSet interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// mysqlDB 是 Authenticator 用到的 *sql.DB 子集。抽这一层的唯一理由是：**本包此前一份测试
// 都没有**，而到期/超额/写回失败回滚/关停顺序这些判定全躲在真实 MySQL 之后，CI 与本机都起
// 不来一个 MySQL。有了这个注入点，同一份同步逻辑就能用假后端逐分支钉住（判据见
// sync_users_test.go）。真实路径仍然走 *sql.DB，方法签名逐字相同。
type mysqlDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (rowSet, error)
	Close() error
}

// sqlBackend 把 *sql.DB 的 QueryContext 返回类型收窄成 rowSet——Go 的接口不接受协变返回，
// 所以这一步是必要的，不是装饰。
type sqlBackend struct{ db *sql.DB }

func (b sqlBackend) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.db.ExecContext(ctx, query, args...)
}

func (b sqlBackend) QueryContext(ctx context.Context, query string, args ...any) (rowSet, error) {
	return b.db.QueryContext(ctx, query, args...) //gosec:disable -- 错误忽略：返回值即调用方的错误判据
}

func (b sqlBackend) Close() error { return b.db.Close() }

type Authenticator struct {
	*memory.Authenticator
	db             mysqlDB
	updateDuration time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func (a *Authenticator) updater() {
	ticker := time.NewTicker(a.updateDuration)
	defer ticker.Stop()

	for {
		a.syncUsers()

		select {
		case <-ticker.C:
		case <-a.ctx.Done():
			log.Debug("MySQL daemon exiting...")
			return
		}
	}
}

// updateTrafficQuery 把内存中的流量增量累加进 users 表。两个方向的取值是**刻意互换**的
// （原注释 "swap upload and download for users"）：upload 列累加 recv、download 列累加 sent。
const updateTrafficQuery = "UPDATE `users` SET `upload`=`upload`+?, `download`=`download`+? WHERE `password`=?;"

// flushUser 把这一个用户内存里尚未刷库的增量写回 users 表。
func (a *Authenticator) flushUser(user statistic.User) {
	hash := user.GetHash()
	sent, recv := user.ResetTraffic()

	_, err := a.db.ExecContext(a.ctx, updateTrafficQuery, recv, sent, hash)
	if err != nil {
		// ResetTraffic 已经把增量清零，写库失败必须原样加回去，否则这段流量
		// 在本轮被丢弃、下一轮从 0 重新累计，永远补不回来（RestoreTraffic 不过限速器）。
		user.RestoreTraffic(sent, recv)
		log.Error(common.NewError("failed to update data to user table").Base(err))
		return
	}
	// 刻意不检查 RowsAffected。MySQL 默认返回“实际改变的行数”（DSN 未启用
	// CLIENT_FOUND_ROWS），本轮增量为 0 的合法用户，UPDATE 影响 0 行；旧代码用
	// `r != 1` 判定“该用户已从库中删除”，于是每一个空闲用户都会在每轮同步里被
	// DelUser 一次——连同它的限速器与 IP 表一并 Close，而活跃连接手里的旧 User
	// 指针仍在记账，却已不在 users 表中，那段流量再也写不回库。
	// 库侧用户的增减由 syncUsers 后半段“DB→内存”的同步统一处理，这里不删。
}

// writeBackTraffic 把内存中自上次同步以来的流量增量写回 users 表。
// syncUsers 的前半段与关停前的最终 flush 都走它（口径只有一处，避免分叉）。
func (a *Authenticator) writeBackTraffic() {
	for _, user := range a.ListUsers() {
		// swap upload and download for users
		a.flushUser(user)
	}
}

// flushAndDelUser 用于「库里已经不该再有这个用户」的三条出口（到期、超额、整行被面板删掉）：
// 先把该用户内存里尚未刷库的增量写回，再删。顺序不能反——DelUser 会调 User.Close()，
// 而 Close 里的 ResetTraffic 只把计数器**清零**、并不写库，反过来的话本轮 writeBackTraffic
// 之后又跑进来的那段流量就永久消失了。
// known 是本轮开始时的内存用户快照：AuthUser 现在会因为到期/超额而拒绝返回用户对象，
// 所以取用户不能靠 AuthUser，只能用 ListUsers。
func (a *Authenticator) flushAndDelUser(known map[string]statistic.User, hash string) {
	if user, ok := known[hash]; ok {
		a.flushUser(user)
	}
	a.DelUser(hash) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
}

// syncUsers 将内存中的流量增量写入数据库，并从数据库同步用户列表到内存。
// 查询中途出错时直接放弃本轮同步：userMap 不完整，据此清理会误删合法用户。
func (a *Authenticator) syncUsers() {
	a.writeBackTraffic()
	log.Info("buffered data has been written into the database")

	// update memory
	rows, err := a.db.QueryContext(a.ctx, "SELECT username,password,quota,download,upload,maxip,expire FROM users")
	if err != nil {
		// 缺列时这里会每轮失败，表现为“流量不回写、用户增减不同步”却又不报致命错。
		// 正常路径下 ensureUsersTable 已在启动期把 maxip / expire 补好，走到这里
		// 说明补列也失败了，需要人工按 ensureUsersTable 里的 ALTER 语句处理。
		log.Error(common.NewError("failed to pull data from the database (users 表缺列；见启动期 ensureUsersTable 的 ALTER TABLE 语句)").Base(err))
		return
	}
	defer rows.Close()

	known := make(map[string]statistic.User)
	for _, user := range a.ListUsers() {
		known[user.GetHash()] = user
	}

	now := time.Now().Unix()
	userMap := make(map[string]bool)
	for rows.Next() {
		var username, hash string
		var maxip int
		var expireAt, quota, download, upload int64
		err := rows.Scan(&username, &hash, &quota, &download, &upload, &maxip, &expireAt)
		if err != nil {
			log.Error(common.NewError("failed to obtain data from the query result").Base(err))
			return
		}
		userMap[hash] = true
		// expire 是 Unix 秒级时间戳，0（或负数）表示永不过期。到期即从内存移除、
		// 拒绝认证，与 quota 用尽是同一个出口。
		if expireAt > 0 && now >= expireAt {
			a.flushAndDelUser(known, hash)
			continue
		}
		if download+upload < quota || quota < 0 {
			a.AddUser(hash)               //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			a.SetKeyShare(hash, username) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			a.SetUserIPLimit(hash, maxip) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			// 把库里的到期时刻与额度推进内存用户。只靠每轮 DelUser 的话，刚到期或刚用尽
			// 的用户在下一轮同步之前（最长一个 check_rate）还能通过认证；memory.Authenticator
			// 的 AuthUser 现在会当场拒绝。SQLite 后端一直是这个语义，两个后端就此对齐。
			a.SetUserExpire(hash, expireAt) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			var usedBase uint64
			if used := download + upload; used > 0 {
				usedBase = uint64(used)
			}
			a.SetUserQuota(hash, quota, usedBase) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		} else {
			a.flushAndDelUser(known, hash)
		}
	}
	if err := rows.Err(); err != nil {
		log.Error(common.NewError("failed to iterate users from the database").Base(err))
		return
	}
	for _, user := range a.ListUsers() {
		if _, ok := userMap[user.GetHash()]; !ok {
			a.flushAndDelUser(known, user.GetHash())
		}
	}
}

func (a *Authenticator) Close() error {
	// 顺序至关重要：必须先停本层 updater 再关内层 memory 认证器。
	// 若先关用户，ResetTraffic 清零后仍在运行的 updater 会把下溢值写进数据库；
	// 且 updater 每轮都会撞上这种脏数据。
	// cancel 之前必须先做一次最终 flush：cancel 之后 updater 退出，而内层
	// memory.Close 的“关停最终 flush”只在配了 sqlite（pst != nil）时才跑，mysql
	// 路径会被整段跳过；紧接着 users.Range(Close) 里的 ResetTraffic 会把最后一轮的
	// 增量清零——不在这里写回，这段流量（最多一个 check_rate 周期）就永久消失。
	a.writeBackTraffic()
	a.cancel()
	a.wg.Wait()
	if err := a.Authenticator.Close(); err != nil {
		log.Error(common.NewError("mysql failed to close memory authenticator").Base(err))
	}
	return a.db.Close()
}

// 连接与单次 I/O 的有界时限。口径：一次全表 users 查询加一轮批量写回，正常部署远到不了
// 30s，而默认 check_rate 也是 30s——卡住时能在一个可感知的周期内报错，而不是永不返回。
const (
	mysqlDialTimeout = 5 * time.Second
	mysqlIOTimeout   = 30 * time.Second
)

// mysqlConfigFrom 把配置段翻译成驱动的结构化连接参数。
//
// 旧实现是手拼 DSN 字符串 user:pass@tcp(host:port)/db?charset=utf8，两处代价：
//  1. 口令里出现 ? / & / @ / : 时，驱动按**第一个 '?'** 切分参数段，User/Passwd/Addr 的解析
//     就被改写，严重时等于把 tls=、allowAllFiles= 这类驱动参数注进连接串。
//  2. 除了 charset 之外没有任何参数，也就是**没有连接/读/写超时**。MySQL 半开、或收下 TCP
//     却不回字节时，syncUsers 里那次 QueryContext 永久阻塞（它的 ctx 只在 Close 才取消），
//     而 updater 是单协程串行：用户增减、quota、**到期判定**和流量写回从此静默停摆，零日志、
//     不自愈。到期判定停摆尤其要命——那正是 issue #102 问的那件事。
//
// 现在直接构造 *mysql.Config 交给 mysql.NewConnector：转义由驱动负责，TLS 配置随连接器走，
// 不再写 mysql.RegisterTLSConfig 那张**包级全局表**（两个认证器用不同 ca 时，后者会悄悄覆盖
// 前者的 "custom"，前者连接池里新建的连接就改按别人的 CA/客户端证书握手）。
func mysqlConfigFrom(username, password, host string, port int, dbName, keyPath, certPath, caPath string) (*mysql.Config, error) {
	cfg := mysql.NewConfig()
	cfg.User = username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	cfg.DBName = dbName
	cfg.Params = map[string]string{"charset": "utf8"}
	cfg.Timeout = mysqlDialTimeout
	cfg.ReadTimeout = mysqlIOTimeout
	cfg.WriteTimeout = mysqlIOTimeout

	if caPath == "" {
		// 与改造前一致：ca 为空 ⇒ 完全不启用 TLS。
		return cfg, nil
	}
	rootCertPool := x509.NewCertPool()
	pem, err := os.ReadFile(caPath) //gosec:disable -- 路径来自配置文件，由用户自己控制，非外部输入
	if err != nil {
		return nil, common.NewError("failed to read the mysql ca file").Base(err)
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return nil, common.NewError("failed to parse the mysql ca file as PEM")
	}
	tlsConf := &tls.Config{
		RootCAs:    rootCertPool,
		MinVersion: tls.VersionTLS12,
	}
	switch {
	case keyPath != "" && certPath != "":
		// 成对给出 ⇒ 带客户端证书。
		clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, common.NewError("failed to load the mysql client key pair").Base(err)
		}
		tlsConf.Certificates = []tls.Certificate{clientCert}
	case keyPath == "" && certPath == "":
		// 都不给 ⇒ 只做服务端校验。
	default:
		// 只给一个，是配置错误。
		return nil, common.NewError("set both key and cert, or set neither")
	}
	cfg.TLS = tlsConf
	return cfg, nil
}

func connectDatabase(m *MySQLConfig) (*sql.DB, error) {
	mc, err := mysqlConfigFrom(m.Username, m.Password, m.ServerHost, m.ServerPort,
		m.Database, m.Key, m.Cert, m.CA)
	if err != nil {
		return nil, common.NewError("failed to build the mysql connection config").Base(err)
	}
	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, common.NewError("failed to create the mysql connector").Base(err)
	}
	return sql.OpenDB(connector), nil
}

func NewAuthenticator(ctx context.Context) (statistic.Authenticator, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	db, err := connectDatabase(&cfg.MySQL)
	if err != nil {
		return nil, common.NewError("Failed to connect to database server").Base(err)
	}
	// 限制连接池:syncUsers 由单个 updater goroutine 串行调用,默认不限最大连接数
	// 时,MySQL 变慢/半开连接会逼出新连接,连接数与 fd 随故障时长无上限增长。
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(time.Minute)
	// 在任何资源分配（memory 认证器、后台协程）之前把表准备好；失败即 fail fast。
	backend := sqlBackend{db: db}
	if err := ensureUsersTable(ctx, backend); err != nil {
		db.Close() //gosec:disable -- 错误忽略：失败路径回收，真实错误由返回值给出
		return nil, err
	}
	memoryAuth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		db.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, err
	}
	// time.NewTicker 对非正周期会 panic，给 check_rate 一个下限
	updateDuration := time.Duration(cfg.MySQL.CheckRate) * time.Second
	if updateDuration <= 0 {
		updateDuration = 30 * time.Second
	}
	// 派生自有 ctx：updater 的生命周期由自身 Close 控制，不依赖调用方取消父 ctx
	ctx, cancel := context.WithCancel(ctx)
	a := &Authenticator{
		db:             backend,
		ctx:            ctx,
		cancel:         cancel,
		updateDuration: updateDuration,
		Authenticator:  memoryAuth.(*memory.Authenticator),
	}
	// 用 wg.Go 而非裸 go：Close() 的 wg.Wait() 必须能等到 updater 退出
	a.wg.Go(a.updater)
	log.Debug("mysql authenticator created")
	return a, nil
}

func init() {
	statistic.RegisterAuthenticatorCreator(Name, NewAuthenticator)
}
