//go:build linux && (amd64 || 386 || arm || arm64)

package sqlite

import (
	"errors"

	"github.com/Potterli20/sqlite"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const Name = "sqlite"

type Persistencer struct {
	db *gorm.DB
}

func NewSqlitePersistencer(path string) (*Persistencer, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
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
