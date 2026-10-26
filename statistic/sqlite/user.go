package sqlite

import (
	"encoding/binary"
)

type User struct {
	Hash string `gorm:"primary_key"`
	// uint64 = 8 byte binary
	Sent      []byte `gorm:"type:TEXT"`
	Recv      []byte `gorm:"type:TEXT"`
	MaxIPNum  int
	Password  string
	SendLimit int
	RecvLimit int
	// Expire 是到期时间（Unix 秒，<= 0 表示永不过期）。语义与 MySQL 后端的
	// users.expire 一致；gorm 的 AutoMigrate 会为新库和已有库补上这一列。
	Expire int64
}

func (u *User) setSent(sent uint64) {
	binary.BigEndian.PutUint64(u.Sent, sent)
}
func (u *User) getSent() uint64 {
	return binary.BigEndian.Uint64(u.Sent)
}

func (u *User) setRecv(recv uint64) {
	binary.BigEndian.PutUint64(u.Recv, recv)
}
func (u *User) getRecv() uint64 {
	return binary.BigEndian.Uint64(u.Recv)
}

func (u *User) GetHash() string {
	return u.Hash
}

func (u *User) GetTraffic() (sent, recv uint64) {
	return u.getSent(), u.getRecv()
}

func (u *User) GetSpeedLimit() (sent, recv int) {
	return u.SendLimit, u.RecvLimit
}

func (u *User) GetIPLimit() int {
	return u.MaxIPNum
}

func (u *User) GetExpire() int64 {
	return u.Expire
}

func (u *User) GetKeyShare() string {
	return u.Password
}
