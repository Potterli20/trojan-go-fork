package zcache

import (
	"context"
	"github.com/Potterli20/golem/pkg/metrics"
	"github.com/stretchr/testify/mock"
	"time"
)

type MockZCache struct {
	mock.Mock
}

func (m *MockZCache) GetStats() ZCacheStats {
	args := m.Called()
	return args.Get(0).(ZCacheStats)
}

func (m *MockZCache) IsNotFoundError(err error) bool {
	args := m.Called(err)
	return args.Bool(0)
}

func (m *MockZCache) SetupAndMonitorMetrics(appName string, metricsServer metrics.TaskMetrics, updateInterval time.Duration) []error {
	args := m.Called(appName, metricsServer, updateInterval)
	return args.Get(0).([]error)
}

func (m *MockZCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	args := m.Called(ctx, key, value, ttl)
	return args.Error(0)
}

func (m *MockZCache) Get(ctx context.Context, key string, data any) error {
	args := m.Called(ctx, key, data)
	return args.Error(0)
}

func (m *MockZCache) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockZCache) Exists(ctx context.Context, keys ...string) (int64, error) {
	args := m.Called(ctx, keys)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) Incr(ctx context.Context, key string) (int64, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) Decr(ctx context.Context, key string) (int64, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) FlushAll(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockZCache) LPush(ctx context.Context, key string, values ...any) (int64, error) {
	args := m.Called(ctx, key, values)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) RPush(ctx context.Context, key string, values ...any) (int64, error) {
	args := m.Called(ctx, key, values)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) SMembers(ctx context.Context, key string) ([]string, error) {
	args := m.Called(ctx, key)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockZCache) SAdd(ctx context.Context, key string, members ...any) (int64, error) {
	args := m.Called(ctx, key, members)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) HSet(ctx context.Context, key string, values ...any) (int64, error) {
	args := m.Called(ctx, key, values)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockZCache) HGet(ctx context.Context, key, field string) (string, error) {
	args := m.Called(ctx, key, field)
	return args.Get(0).(string), args.Error(1)
}

func (m *MockZCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockZCache) ZIncrBy(ctx context.Context, key string, member string, increment float64) (float64, error) {
	args := m.Called(ctx, key, member, increment)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockZCache) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]CustomZ, error) {
	args := m.Called(ctx, key, start, stop)
	return args.Get(0).([]CustomZ), args.Error(1)
}

func (m *MockZCache) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	args := m.Called(ctx, key, ttl)
	return args.Get(0).(bool), args.Error(1)
}
