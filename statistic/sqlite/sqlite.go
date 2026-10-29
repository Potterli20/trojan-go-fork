//go:build linux && (amd64 || 386 || arm || arm64)

package sqlite

import (
	"errors"
	"fmt"
	"time"

	"github.com/Potterli20/sqlite"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const Name = "sqlite"

// gormWriter 把 gorm 的 SQL 日志接进本仓的日志链，而不是让它直接写 os.Stdout。
//
// 必须接管：`&gorm.Config{}` 会落到 gorm 的 `logger.Default`——它写 os.Stdout、级别 Warn，
// 而 Warn 输出的内容正是慢查询（默认 >200ms）与失败语句的**完整 SQL 文本，变量已内插**
// （gorm logger/logger.go:76 定义 Default，回调把内插后的串交给 Trace）。这不是理论风险：
// 本后端的 `password` 列存的 `u.GetKeyShare()`，而 statistic/memory 里那个 getter 返回的是
// **原始明文口令**，不是 hash。于是一次写库超过 200ms、或库锁冲突重试（memory 那边专门
// 为 DB-locked 写了退避重试，说明这条路常走），明文口令就按 SQL 原文打到 stdout，
// 完全绕过 log 包的整条清洗链——CI 日志里已经能看到它的输出形态：
// `[0.056ms] [rows:0] SELECT * FROM `users` WHERE hash = "hash-a" ...`（值已内插）。
//
// 走 log.Warn 之后，INSERT 那一行必然带 `password` 这个列名 ⇒ 命中 SensitiveKeywords
// 整行打码，口令出不去；而真正的数据库错误仍旧经 db.Error 原样返回给调用方，诊断一点没丢。
// Colorful 关掉：ANSI 控制码落进日志是给下游解析埋雷。
type gormWriter struct{}

func (gormWriter) Printf(format string, v ...any) {
	log.Warn(fmt.Sprintf("sqlite: "+format, v...))
}

// gormLogger 与 gorm 的 stdout 默认值同级别（Warn：只报慢查询与失败语句），
// 只是换了目的地。
func newGormLogger() gormlogger.Interface {
	return gormlogger.New(gormWriter{}, gormlogger.Config{
		SlowThreshold: 200 * time.Millisecond,
		LogLevel:      gormlogger.Warn,
		// First() 查不到用户是本仓的正常控制流（调用方按 err 分支处理），
		// 不该每次都往日志里刷一行带内插值的 SQL。
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

type Persistencer struct {
	db *gorm.DB
}

func NewSqlitePersistencer(path string) (*Persistencer, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: newGormLogger()})
	if err != nil {
		return nil, err
	}
	err = db.AutoMigrate(&User{})
	if err != nil {
		// gorm.Open 已经建好并持有一个 *sql.DB（AutoMigrate 必然开过连接），
		// 这里不关掉就把 sqlite 文件句柄和连接池留在进程里。建库失败往往是反复
		// 重试的路径（文件被占、schema 不兼容），每次漏一份。
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			sqlDB.Close() //gosec:disable -- 错误忽略：失败路径回收，真实错误由返回值给出
		}
		return nil, err
	}
	sp := &Persistencer{
		db: db,
	}

	return sp, nil
}

func (p *Persistencer) SaveUser(u statistic.Metadata) error {
	if u == nil {
		return errors.New("user is nil")
	}
	ls, lr := u.GetSpeedLimit()
	usr := &User{
		Hash:      u.GetHash(),
		MaxIPNum:  u.GetIPLimit(),
		Password:  u.GetKeyShare(),
		SendLimit: ls,
		RecvLimit: lr,
		Expire:    u.GetExpire(),
		Sent:      make([]byte, 8),
		Recv:      make([]byte, 8),
	}
	ts, tr := u.GetTraffic()
	usr.setSent(ts)
	usr.setRecv(tr)
	err := p.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "hash"}},
		UpdateAll: true,
	}).Create(usr).Error
	if err != nil {
		return err
	}
	return nil
}

func (p *Persistencer) LoadUser(hash string) (statistic.Metadata, error) {
	var u User
	err := p.db.First(&u, "hash = ?", hash).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (p *Persistencer) DeleteUser(hash string) error {
	err := p.db.Delete(&User{Hash: hash}).Error
	if err != nil {
		return err
	}
	return nil
}

func (p *Persistencer) ListUser(f func(hash string, u statistic.Metadata) bool) error {
	users := make([]User, 0)
	err := p.db.Find(&users).Error
	if err != nil {
		return err
	}
	for _, u := range users {
		if goOn := f(u.Hash, &u); !goOn {
			break
		}
	}
	return nil
}

func (p *Persistencer) UpdateUserTraffic(hash string, sent, recv uint64) error {
	u := &User{
		Hash: hash,
		Sent: make([]byte, 8),
		Recv: make([]byte, 8),
	}
	u.setSent(sent)
	u.setRecv(recv)
	return p.db.Model(&User{Hash: hash}).Updates(u).Error
}

// Close 关闭底层数据库连接池,释放 sqlite 文件句柄与 *sql.DB 空闲连接。
// 之前 Persistencer 无 Close,memory.Authenticator 也从不关闭 a.pst,导致
// Authenticator.Close()(测试清理、多实例、将来热重载)时泄漏 DB/FD。
func (p *Persistencer) Close() error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
