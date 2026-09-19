//go:build !linux || !(amd64 || 386 || arm || arm64)

package sqlite

import (
	"errors"

	"github.com/Potterli20/trojan-go-fork/statistic"
)

const Name = "sqlite"

// Persistencer 在不支持的平台上是空实现，仅保证跨平台编译通过。
// 见 NewSqlitePersistencer：这里不会返回可用实例，所有方法都不应被调用。
type Persistencer struct{}

// NewSqlitePersistencer 在不支持的平台直接失败。
// 旧实现返回一个空的 Persistencer 且 error 为 nil，memory 认证器会照常打出
// 「已启用持久化后端」的日志，用户以为流量和用户已经落盘，实际全部写入空实现、
// 进程重启即丢失。宁可在启动阶段明确失败，也不静默降级。
func NewSqlitePersistencer(_ string) (*Persistencer, error) {
	return nil, errors.New("sqlite persistence is only supported on linux amd64/386/arm/arm64; current build does not include the driver")
}

func (p *Persistencer) SaveUser(u statistic.Metadata) error {
	return nil
}

func (p *Persistencer) LoadUser(hash string) (statistic.Metadata, error) {
	var u User
	return &u, nil
}

func (p *Persistencer) DeleteUser(hash string) error {
	return nil
}

func (p *Persistencer) ListUser(f func(hash string, u statistic.Metadata) bool) error {
	return nil
}

func (p *Persistencer) UpdateUserTraffic(hash string, sent, recv uint64) error {
	return nil
}

// Close 非 linux stub:无真实数据库句柄,空实现以满足 statistic.Persistencer 接口。
func (p *Persistencer) Close() error {
	return nil
}
