package zcache

import (
	"context"
	"encoding/json"
	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"time"

	"github.com/go-redis/redis/v8"
)

type CustomZ struct {
	Score  float64
	Member any
}

type RedisStats struct {
	Pool *redis.PoolStats
}

type RemoteCache interface {
	ZCache
	Incr(ctx context.Context, key string) (int64, error)
	Decr(ctx context.Context, key string) (int64, error)
	LPush(ctx context.Context, key string, values ...any) (int64, error)
	RPush(ctx context.Context, key string, values ...any) (int64, error)
	SMembers(ctx context.Context, key string) ([]string, error)
	SAdd(ctx context.Context, key string, members ...any) (int64, error)
	HSet(ctx context.Context, key string, values ...any) (int64, error)
	HGet(ctx context.Context, key, field string) (string, error)
	ZIncrBy(ctx context.Context, key string, member string, increment float64) (float64, error)
	ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]CustomZ, error)
	FlushAll(ctx context.Context) error
	Exists(ctx context.Context, keys ...string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	TTL(ctx context.Context, key string) (time.Duration, error)
}

type redisCache struct {
	client        *redis.Client
	prefix        string
	logger        *logger.Logger
	metricsServer metrics.TaskMetrics
}

func (c *redisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("set key on redis cache, fullKey: [%s], value: [%v]", realKey, value)

	val, err := json.Marshal(value)
	if err != nil {
		return err
	}

	err = c.client.Set(ctx, realKey, val, ttl).Err()
	if err != nil {
		c.logger.Errorf("error setting new key on redis cache, fullKey: [%s], err: [%s]", realKey, err)
	}

	return err
}

func (c *redisCache) Get(ctx context.Context, key string, data any) error {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("get key on redis cache, fullKey: [%s]", realKey)

	val, err := c.client.Get(ctx, realKey).Result()
	if err != nil {
		if c.IsNotFoundError(err) {
			c.logger.Debugf("key not found on redis cache, fullKey: [%s]", realKey)
		} else {
			c.logger.Errorf("error getting key on redis cache, fullKey: [%s], err: [%s]", realKey, err)
		}
		return err
	}
	return json.Unmarshal([]byte(val), &data)
}

func (c *redisCache) Delete(ctx context.Context, key string) error {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("delete key on redis cache, fullKey: [%s]", realKey)

	return c.client.Del(ctx, realKey).Err()
}

func (c *redisCache) Exists(ctx context.Context, keys ...string) (int64, error) {
	realKeys := getKeysWithPrefix(c.prefix, keys)

	c.logger.Debugf("exists keys on redis cache, fullKeys: [%s]", realKeys)

	return c.client.Exists(ctx, realKeys...).Result()
}

func (c *redisCache) Incr(ctx context.Context, key string) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("increment on key on redis cache, fullKey: [%s]", realKey)
	return c.client.Incr(ctx, realKey).Result()
}

func (c *redisCache) Decr(ctx context.Context, key string) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("decrement on key on redis cache, fullKey: [%s]", realKey)
	return c.client.Decr(ctx, realKey).Result()
}

func (c *redisCache) FlushAll(ctx context.Context) error {
	c.logger.Debugf("flush all on redis cache, fullKey")
	return c.client.FlushAll(ctx).Err()
}

func (c *redisCache) LPush(ctx context.Context, key string, values ...any) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("lpush on redis cache, fullKey: [%s]", realKey)
	return c.client.LPush(ctx, realKey, values...).Result()
}

func (c *redisCache) RPush(ctx context.Context, key string, values ...any) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("rpush on redis cache, fullKey: [%s]", realKey)
	return c.client.RPush(ctx, realKey, values...).Result()
}

func (c *redisCache) SMembers(ctx context.Context, key string) ([]string, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("smemebers on redis cache, fullKey: [%s]", realKey)
	return c.client.SMembers(ctx, realKey).Result()
}

func (c *redisCache) SAdd(ctx context.Context, key string, members ...any) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("sadd on redis cache, fullKey: [%s]", realKey)
	return c.client.SAdd(ctx, realKey, members...).Result()
}

func (c *redisCache) HSet(ctx context.Context, key string, values ...any) (int64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("hset on redis cache, fullKey: [%s]", realKey)
	return c.client.HSet(ctx, realKey, values...).Result()
}

func (c *redisCache) HGet(ctx context.Context, key, field string) (string, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("hget on redis cache, fullKey: [%s]", realKey)
	return c.client.HGet(ctx, realKey, field).Result()
}

func (c *redisCache) ZIncrBy(ctx context.Context, key string, member string, increment float64) (float64, error) {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("ZIncrBy on key in redis cache, fullKey: [%s], member: [%s], increment: [%f]", realKey, member, increment)
	return c.client.ZIncrBy(ctx, realKey, increment, member).Result()
}

func (c *redisCache) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]CustomZ, error) {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("ZRevRangeWithScores on key in redis cache, fullKey: [%s], start: [%d], stop: [%d]", realKey, start, stop)
	zSlice, err := c.client.ZRevRangeWithScores(ctx, realKey, start, stop).Result()
	if err != nil {
		return nil, err
	}

	var customZSlice []CustomZ
	for _, z := range zSlice {
		customZSlice = append(customZSlice, CustomZ{
			Member: z.Member,
			Score:  z.Score,
		})
	}

	return customZSlice, nil
}

func (c *redisCache) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	realKey := getKeyWithPrefix(c.prefix, key)

	c.logger.Debugf("Expire on key in redis cache, fullKey: [%s], member: [%s], increment: [%f]", realKey)
	return c.client.Expire(ctx, realKey, ttl).Result()
}

func (c *redisCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	realKey := getKeyWithPrefix(c.prefix, key)
	c.logger.Debugf("ttl on redis cache, realKey: [%s]", realKey)

	return c.client.TTL(ctx, realKey).Result()
}

func (c *redisCache) GetStats() ZCacheStats {
	poolStats := c.client.PoolStats()
	c.logger.Debugf("redis cache pool stats: [%v]", poolStats)

	ctx := context.Background()
	stats, err := c.client.Info(ctx).Result()
	if err != nil {
		c.logger.Errorf("error on redis cache stats: [%v]", stats)
	}

	c.logger.Debugf("redis cache stats: \n %s", stats)
	// ctx := context.Background()
	// stats, _ := c.client.Info(ctx).Result()

	return ZCacheStats{
		Remote: &RedisStats{
			Pool: poolStats,
		},
	}
}

func (c *redisCache) IsNotFoundError(err error) bool {
	return err.Error() == "redis: nil"
}

func (c *redisCache) setupAndMonitorMetrics(updateInterval time.Duration) {
	setupAndMonitorCacheMetrics(c.metricsServer, c, c.logger, updateInterval)
}
