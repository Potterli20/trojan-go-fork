package zcache

import (
	"time"

	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"github.com/dgraph-io/ristretto"
	"github.com/go-redis/redis/v8"
)

const (
	// Default Ristretto cache config
	DefaultNumCounters = int64(1e7)  // 10M keys
	DefaultMaxCostMB   = int64(1024) // 1GB
	DefaultBufferItems = int64(64)
)

type StatsMetrics struct {
	Enable         bool
	UpdateInterval time.Duration
}

type RemoteConfig struct {
	Network            string
	Addr               string
	Password           string
	DB                 int
	DialTimeout        time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	PoolSize           int
	MinIdleConns       int
	MaxConnAge         time.Duration
	PoolTimeout        time.Duration
	IdleTimeout        time.Duration
	IdleCheckFrequency time.Duration
	Prefix             string
	Logger             *logger.Logger
	MetricServer       metrics.TaskMetrics
	StatsMetrics       StatsMetrics
}

type LocalConfig struct {
	Prefix       string
	Logger       *logger.Logger
	MetricServer metrics.TaskMetrics
	StatsMetrics StatsMetrics

	// Add Ristretto cache configuration
	NumCounters int64 `json:"num_counters"` // default: 1e7
	MaxCostMB   int64 `json:"max_cost_mb"`  // in MB, default: 1024 (1GB)
	BufferItems int64 `json:"buffer_items"` // default: 64
}

func (c *RemoteConfig) ToRedisConfig() *redis.Options {
	return &redis.Options{
		Network:            c.Network,
		Addr:               c.Addr,
		Password:           c.Password,
		DB:                 c.DB,
		DialTimeout:        c.DialTimeout,
		ReadTimeout:        c.ReadTimeout,
		WriteTimeout:       c.WriteTimeout,
		PoolSize:           c.PoolSize,
		MinIdleConns:       c.MinIdleConns,
		MaxConnAge:         c.MaxConnAge,
		PoolTimeout:        c.PoolTimeout,
		IdleTimeout:        c.IdleTimeout,
		IdleCheckFrequency: c.IdleCheckFrequency,
	}
}

func (c *LocalConfig) ToRistrettoConfig() *ristretto.Config {
	numCounters := c.NumCounters
	if numCounters == 0 {
		numCounters = DefaultNumCounters
	}

	maxCostMB := c.MaxCostMB
	if maxCostMB == 0 {
		maxCostMB = DefaultMaxCostMB
	}
	// Convert MB to bytes
	maxCost := maxCostMB << 20

	bufferItems := c.BufferItems
	if bufferItems == 0 {
		bufferItems = DefaultBufferItems
	}

	return &ristretto.Config{
		NumCounters: numCounters,
		MaxCost:     maxCost,
		BufferItems: bufferItems,
	}
}

type CombinedConfig struct {
	Local              *LocalConfig
	Remote             *RemoteConfig
	GlobalLogger       *logger.Logger
	GlobalPrefix       string
	GlobalMetricServer metrics.TaskMetrics
	GlobalStatsMetrics StatsMetrics
	IsRemoteBestEffort bool
}
