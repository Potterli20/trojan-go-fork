package statistic

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

const Name = "STATISTICS"

type Metadata interface {
	GetHash() string
	GetKeyShare() string
	GetTraffic() (sent, recv uint64)
	GetSpeedLimit() (sent, recv int)
	GetIPLimit() int
	// GetExpire 返回到期时间（Unix 秒，<= 0 表示永不过期）。持久化后端用它把
	// “按时到期”带给内存认证器；配置里的内存用户恒为 0。
	GetExpire() int64
}

type TrafficMeter interface {
	io.Closer
	AddSentTraffic(sent int)
	AddRecvTraffic(recv int)
	// RestoreTraffic 与 ResetTraffic 对称：把 ResetTraffic 取走、但写库失败的增量
	// 原样加回计数器。它是账目回滚、不是新流量，所以不经过限速器。
	RestoreTraffic(sent, recv uint64)
	ResetTraffic() (sent, recv uint64)
	GetSpeed() (sent, recv uint64)
}

type IPRecorder interface {
	AddIP(string) bool
	DelIP(string) bool
	GetIP() int
}

type User interface {
	Metadata
	TrafficMeter
	IPRecorder
}

type Persistencer interface {
	io.Closer
	SaveUser(Metadata) error
	LoadUser(hash string) (Metadata, error)
	DeleteUser(hash string) error
	ListUser(func(hash string, u Metadata) bool) error
	UpdateUserTraffic(hash string, sent, recv uint64) error
}

type Authenticator interface {
	io.Closer
	AuthUser(hash string) (valid bool, user User)
	AddUser(hash string) error
	DelUser(hash string) error
	SetUserTraffic(hash string, sent, recv uint64) error
	SetUserSpeedLimit(hash string, send, recv int) error
	SetUserIPLimit(hash string, limit int) error
	ListUsers() []User
}

type Creator func(ctx context.Context) (Authenticator, error)

var (
	createdAuthLock sync.Mutex
	authCreators    = make(map[string]Creator)
	createdAuth     = make(map[context.Context]Authenticator)
)

func RegisterAuthenticatorCreator(name string, creator Creator) {
	authCreators[name] = creator
}

func NewAuthenticator(ctx context.Context, name string) (Authenticator, error) {
	// allocate a unique authenticator for each context
	createdAuthLock.Lock() // avoid concurrent map read/write
	defer createdAuthLock.Unlock()
	if auth, found := createdAuth[ctx]; found {
		log.Debug("authenticator has been created:", name)
		return auth, nil
	}
	creator, found := authCreators[strings.ToUpper(name)]
	if !found {
		return nil, common.NewError("auth driver name " + name + " not found")
	}
	auth, err := creator(ctx)
	if err != nil {
		return nil, err
	}
	createdAuth[ctx] = auth
	return auth, err
}

// ReleaseAuthenticator 关闭并移除 ctx 对应的认证器实例。
// createdAuth 只增不删，不释放会让已关闭的实例（连同用户表和数据库句柄）
// 被这个全局 map 永久持有。
// CreatedAuthenticatorCount 返回全局表里当前登记的认证器数量。createdAuth 只增不删，
// 这张表就是"实例建了却没释放"这类回归最先显形的地方，所以提供一个只读观测点。
func CreatedAuthenticatorCount() int {
	createdAuthLock.Lock()
	defer createdAuthLock.Unlock()
	return len(createdAuth)
}

func ReleaseAuthenticator(ctx context.Context) error {
	createdAuthLock.Lock()
	auth, found := createdAuth[ctx]
	if found {
		delete(createdAuth, ctx)
	}
	createdAuthLock.Unlock()
	if !found {
		return nil
	}
	return auth.Close()
}
