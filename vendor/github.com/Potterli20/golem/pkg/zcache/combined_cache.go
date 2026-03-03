package zcache

import (
	"context"
	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"time"
)

type CombinedCache interface {
	ZCache
}

type combinedCache struct {
	localCache         LocalCache
	remoteCache        RemoteCache
	logger             *logger.Logger
	isRemoteBestEffort bool
	metricsServer      metrics.TaskMetrics
}

func (c *combinedCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	c.logger.Debugf("set key on combined cache, key: [%s]", key)

	if err := c.remoteCache.Set(ctx, key, value, ttl); err != nil {
		c.logger.Errorf("error setting key on combined/remote cache, key: [%s], err: %s", key, err)
		if !c.isRemoteBestEffort {
			c.logger.Debugf("emitting error as remote best effort is false, key: [%s]", key)
			return err
		}
	}

	if err := c.localCache.Set(ctx, key, value, ttl); err != nil {
		c.logger.Errorf("error setting key on combined/local cache, key: [%s], err: %s", key, err)
		return err
	}
	return nil
}

func (c *combinedCache) Get(ctx context.Context, key string, data any) error {
	c.logger.Debugf("get key on combined cache, key: [%s]", key)

	err := c.localCache.Get(ctx, key, data)
	if err != nil {
		if c.localCache.IsNotFoundError(err) {
			c.logger.Debugf("key not found on combined/local cache, key: [%s]", key)
		} else {
			c.logger.Debugf("error getting key on combined/local cache, key: [%s], err: %s", key, err)
		}

		if err := c.remoteCache.Get(ctx, key, data); err != nil {
			if c.remoteCache.IsNotFoundError(err) {
				c.logger.Debugf("key not found on combined/remote cache, key: [%s]", key)
			} else {
				c.logger.Debugf("error getting key on combined/remote cache, key: [%s], err: %s", key, err)
			}

			return err
		}

		c.logger.Debugf("set value found on remote cache in the local cache, key: [%s]", key)
		ttl, ttlErr := c.remoteCache.TTL(ctx, key)

		// Refresh data TTL on both caches
		if ttlErr == nil {
			_ = c.localCache.Set(ctx, key, data, ttl)
		} else {
			c.logger.Errorf("error getting TTL for key [%s] from remote cache, err: %s", key, ttlErr)
		}
	}

	return nil
}

func (c *combinedCache) Delete(ctx context.Context, key string) error {
	c.logger.Debugf("delete key on combined cache, key: [%s]", key)
	err2 := c.remoteCache.Delete(ctx, key)
	if err2 != nil {
		c.logger.Errorf("error deleting key on combined/remote cache, key: [%s], err: %s", key, err2)
		if !c.isRemoteBestEffort {
			c.logger.Debugf("emitting error as remote best effort is false, key: [%s]")
			return err2
		}
	}

	if err1 := c.localCache.Delete(ctx, key); err1 != nil {
		c.logger.Errorf("error deleting key on combined/local cache, key: [%s], err: %s", key, err1)
		return err1
	}

	return nil
}

func (c *combinedCache) GetStats() ZCacheStats {
	localStats := c.localCache.GetStats()
	remotePoolStats := c.remoteCache.GetStats()
	return ZCacheStats{
		Local:  localStats.Local,
		Remote: remotePoolStats.Remote,
	}
}

func (c *combinedCache) IsNotFoundError(err error) bool {
	return c.remoteCache.IsNotFoundError(err) || c.localCache.IsNotFoundError(err)
}

func (c *combinedCache) setupAndMonitorMetrics(updateInterval time.Duration) {
	setupAndMonitorCacheMetrics(c.metricsServer, c, c.logger, updateInterval)
}
