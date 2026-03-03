package zcache

import (
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v8"
	"time"
)

type ZMutex interface {
	Lock() error
	Unlock() (bool, error)
	Name() string
}

type zMutex struct {
	mutex *redsync.Mutex
}

func (c *redisCache) NewMutex(name string, expiry time.Duration) ZMutex {
	pool := goredis.NewPool(c.client)
	rs := redsync.New(pool)

	mutex := rs.NewMutex(name, redsync.WithExpiry(expiry))
	return &zMutex{mutex: mutex}
}

func (m *zMutex) Lock() error {
	return m.mutex.Lock()
}

func (m *zMutex) Unlock() (bool, error) {
	return m.mutex.Unlock()
}

func (m *zMutex) Name() string {
	return m.mutex.Name()
}
