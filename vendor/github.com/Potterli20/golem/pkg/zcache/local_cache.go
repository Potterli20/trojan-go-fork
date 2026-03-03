package zcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"github.com/dgraph-io/ristretto"
)

//nolint:unused,varcheck,deadcode
const (
	neverExpires = -1
	cacheCost    = 1
)

type CacheItem struct {
	Value     []byte `json:"value"`
	ExpiresAt int64  `json:"expires_at"`
}

func NewCacheItem(value []byte, ttl time.Duration) CacheItem {
	expiresAt := time.Now().Add(ttl).Unix()
	if ttl < 0 {
		expiresAt = neverExpires
	}

	return CacheItem{
		Value:     value,
		ExpiresAt: expiresAt,
	}
}

func (item CacheItem) IsExpired() bool {
	if item.ExpiresAt < 0 {
		return false
	}
	return time.Now().Unix() > item.ExpiresAt
}

type LocalCache interface {
	ZCache
}

type localCache struct {
	client        *ristretto.Cache
	prefix        string
	logger        *logger.Logger
	metricsServer metrics.TaskMetrics
}

func (c *localCache) Set(_ context.Context, key string, value any, ttl time.Duration) error {
	realKey := getKeyWithPrefix(c.prefix, key)

	b, err := json.Marshal(value)
	if err != nil {
		c.logger.Errorf("error marshalling value, key: [%s], err: [%s]", realKey, err)
		return err
	}

	// Handle never expires case
	if ttl < 0 {
		ttl = 0 // 0 means never expire in Ristretto
	}

	if !c.client.SetWithTTL(realKey, b, cacheCost, ttl) {
		c.logger.Errorf("error setting new key on local cache, fullKey: [%s]", realKey)
		return errors.New("failed to set key with TTL")
	}

	c.client.Wait()
	return nil
}

func (c *localCache) Get(_ context.Context, key string, data any) error {
	realKey := getKeyWithPrefix(c.prefix, key)

	val, found := c.client.Get(realKey)
	if !found {
		c.logger.Debugf("key not found on local cache, fullKey: [%s]", realKey)
		return errors.New("cache miss")
	}

	return json.Unmarshal(val.([]byte), data)
}

// Delete removes a value from the cache
func (c *localCache) Delete(ctx context.Context, key string) error {
	if c.client == nil {
		return fmt.Errorf("cache client is not initialized")
	}
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("delete key on local cache, fullKey: [%s]", realKey)
	c.client.Del(realKey)
	return nil
}

func (c *localCache) GetStats() ZCacheStats {
	stats := c.client.Metrics
	c.logger.Debugf("local cache stats: [%v]", stats)

	return ZCacheStats{Local: stats}
}

func (c *localCache) IsNotFoundError(err error) bool {
	return err != nil && err.Error() == "cache miss"
}

func (c *localCache) setupAndMonitorMetrics(updateInterval time.Duration) {
	setupAndMonitorCacheMetrics(c.metricsServer, c, c.logger, updateInterval)
}
