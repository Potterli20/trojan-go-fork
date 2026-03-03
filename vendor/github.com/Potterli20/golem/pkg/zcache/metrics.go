package zcache

import (
	"time"

	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"github.com/Potterli20/golem/pkg/metrics/collectors"
	"go.uber.org/zap"
)

const (
	defaultInterval                = time.Minute
	localCacheHitsMetricName       = "local_cache_hits"
	localCacheMissesMetricName     = "local_cache_misses"
	localCacheDelHitsMetricName    = "local_cache_del_hits"
	localCacheDelMissesMetricName  = "local_cache_del_misses"
	localCacheCollisionsMetricName = "local_cache_collisions"

	remoteCachePoolHitsMetricName       = "remote_cache_pool_hits"
	remoteCachePoolMissesMetricName     = "remote_cache_pool_misses"
	remoteCachePoolTimeoutsMetricName   = "remote_cache_pool_timeouts"
	remoteCachePoolTotalConnsMetricName = "remote_cache_pool_total_conns"
	remoteCachePoolIdleConnsMetricName  = "remote_cache_pool_idle_conns"
	remoteCachePoolStaleConnsMetricName = "remote_cache_pool_stale_conns"
)

func setupAndMonitorCacheMetrics(metricsServer metrics.TaskMetrics, cache ZCache, logger *logger.Logger, updateInterval time.Duration) {
	if updateInterval <= 0 {
		updateInterval = defaultInterval
	}

	// Register cache metrics with error handling
	registerMetric(metricsServer, localCacheHitsMetricName, "Number of successfully found keys", logger)
	registerMetric(metricsServer, localCacheMissesMetricName, "Number of not found keys", logger)
	registerMetric(metricsServer, localCacheDelHitsMetricName, "Number of successfully deleted keys", logger)
	registerMetric(metricsServer, localCacheDelMissesMetricName, "Number of not deleted keys", logger)
	registerMetric(metricsServer, localCacheCollisionsMetricName, "Number of key collisions", logger)

	registerMetric(metricsServer, remoteCachePoolHitsMetricName, "Number of times free connection was found in the pool", logger)
	registerMetric(metricsServer, remoteCachePoolMissesMetricName, "Number of times free connection was NOT found in the pool", logger)
	registerMetric(metricsServer, remoteCachePoolTimeoutsMetricName, "Number of wait timeout occurrences", logger)
	registerMetric(metricsServer, remoteCachePoolTotalConnsMetricName, "Total connections in the pool", logger)
	registerMetric(metricsServer, remoteCachePoolIdleConnsMetricName, "Idle connections in the pool", logger)
	registerMetric(metricsServer, remoteCachePoolStaleConnsMetricName, "Stale connections removed from the pool", logger)

	go func() {
		ticker := time.NewTicker(updateInterval)
		defer ticker.Stop()
		for range ticker.C {
			stats := cache.GetStats()

			if stats.Local != nil {
				_ = metricsServer.UpdateMetric(localCacheHitsMetricName, float64(stats.Local.Hits()))
				_ = metricsServer.UpdateMetric(localCacheMissesMetricName, float64(stats.Local.Misses()))
				// _ = metricsServer.UpdateMetric(localCacheCollisionsMetricName, float64(stats.Local.Collisions()))
			}

			if stats.Remote != nil {
				_ = metricsServer.UpdateMetric(remoteCachePoolHitsMetricName, float64(stats.Remote.Pool.Hits))
				_ = metricsServer.UpdateMetric(remoteCachePoolMissesMetricName, float64(stats.Remote.Pool.Misses))
				_ = metricsServer.UpdateMetric(remoteCachePoolTimeoutsMetricName, float64(stats.Remote.Pool.Timeouts))
				_ = metricsServer.UpdateMetric(remoteCachePoolTotalConnsMetricName, float64(stats.Remote.Pool.TotalConns))
				_ = metricsServer.UpdateMetric(remoteCachePoolIdleConnsMetricName, float64(stats.Remote.Pool.IdleConns))
				_ = metricsServer.UpdateMetric(remoteCachePoolStaleConnsMetricName, float64(stats.Remote.Pool.StaleConns))
			}
		}
	}()
}

// Helper function to register a metric if it's not already registered
func registerMetric(metricsServer metrics.TaskMetrics, metricName, description string, logger *logger.Logger) {
	// Register the metric if it hasn't been registered yet
	if err := metricsServer.RegisterMetric(metricName, description, nil, &collectors.Gauge{}); err != nil {
		logger.Errorf("Failed to register cache stats metrics for %s, err: %s", metricName, zap.Error(err))
	}
}
