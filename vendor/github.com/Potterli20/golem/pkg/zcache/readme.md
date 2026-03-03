# zcache Package

## Overview
The `zcache` package provides an abstraction layer over Redis, allowing easy integration of caching mechanisms into Go applications. It simplifies interacting with Redis by offering a common interface for various caching operations.

## Table of Contents
1. [Features](#features)
2. [Installation](#installation)
3. [Usage](#usage)
4. [Configuration](#configuration)
5. [Mocking Support](#mocking-support)

## Features
- **Unified Caching Interface**: Offers a consistent API for common caching operations, abstracting the complexity of direct Redis interactions.
- **Distributed Mutex Locks**: Supports distributed synchronization using Redis-based mutex locks, crucial for concurrent operations.
- **Extensibility**: Easy to extend with additional methods for more Redis operations.
- **Serialization and Deserialization**: Automatically handles the conversion of Go data structures to and from Redis storage formats.
- **Mocking for Testing**: Includes mock implementations for easy unit testing without a live Redis instance.
- **Connection Pool Management**: Efficiently handles Redis connection pooling.
- **Supported Operations**: Includes a variety of caching operations like Set, Get, Delete, as well as more advanced operations like Incr, Decr, and others.

---

## Installation
```bash
go get github.com/zondax/golem/pkg/zcache
```

---

## Usage Remote cache - Redis

```go
import (
    "github.com/zondax/golem/pkg/zcache"
    "context"
    "time"
)

func main() {
    config := zcache.RemoteConfig{Addr: "localhost:6379"}
    cache := zcache.NewRemoteCache(config)
    ctx := context.Background()

    // Set a value
    cache.Set(ctx, "key1", "value1", 10*time.Minute)

    // Get a value
    if value, err := cache.Get(ctx, "key1"); err == nil {
        fmt.Println("Retrieved value:", value)
    }

    // Delete a value
    cache.Delete(ctx, "key1")
}
```


## Usage Local cache - Ristretto

The LocalConfig for zcache provides configuration for the Ristretto cache. Ristretto is a high-performance memory-bound cache with built-in metrics and automatic memory management.

It's important to note that MetricServer is a mandatory configuration field in LocalConfig to facilitate the monitoring of cache operations and errors.

```go
func main() {
    config := zcache.LocalConfig{
        // Ristretto cache configuration
        NumCounters: 1e7,         // Number of keys to track frequency (default: 10M)
        MaxCostMB:   1024,        // Maximum cost of cache in MB (default: 1024MB/1GB)
        BufferItems: 64,          // Number of keys per Get buffer (default: 64)
        
        // Metrics are required
        MetricServer: metricServer, 
    }
    
    cache, err := zcache.NewLocalCache(&config)
    if err != nil {
        // Handle error
    }
    
    ctx := context.Background()
    
    cache.Set(ctx, "key1", "value1", 10*time.Minute)
    if value, err := cache.Get(ctx, "key1"); err == nil {
        fmt.Println("Retrieved value:", value)
    }
    cache.Delete(ctx, "key1")
}

```


## Usage Combined cache - Local and Remote

```go
func main() {
    localConfig := zcache.LocalConfig{
        // Ristretto cache configuration
        NumCounters: 1e7,          // Number of keys to track (default: 10M)
        MaxCostMB:   512,          // Max memory usage - 512MB
        BufferItems: 64,           // Size of Get buffer
        
        // Metrics are required
        MetricServer: metricServer,
    }
    remoteConfig := zcache.RemoteConfig{Addr: "localhost:6379"}
    config := zcache.CombinedConfig{Local: localConfig, Remote: remoteConfig, isRemoteBestEffort: false}
    cache, err := zcache.NewCombinedCache(config)
    if err != nil {
        // Handle error
    }
    
    ctx := context.Background()
    
    cache.Set(ctx, "key1", "value1", 10*time.Minute)
    if value, err := cache.Get(ctx, "key1"); err == nil {
        fmt.Println("Retrieved value:", value)
    }
    cache.Delete(ctx, "key1")
}

```

--- 

## Configuration 

Configure zcache using the Config struct, which includes network settings, server address, timeouts, and other connection parameters. This struct allows you to customize the behavior of your cache and mutex instances to fit your application's needs.

```go
type Config struct {
    Addr             string        // Redis server address
    Password         string        // Redis server password
    DB               int           // Redis database
    DialTimeout      time.Duration // Timeout for connecting to Redis
    ReadTimeout      time.Duration // Timeout for reading from Redis
    WriteTimeout     time.Duration // Timeout for writing to Redis
    PoolSize         int           // Number of connections in the pool
    MinIdleConns     int           // Minimum number of idle connections
    IdleTimeout      time.Duration // Timeout for idle connections
}
```
---

## Working with mutex

```go
func main() {
    cache := zcache.NewCache(zcache.Config{Addr: "localhost:6379"})
    mutex := cache.NewMutex("mutex_name", 2*time.Minute)

    // Acquire lock
    if err := mutex.Lock(); err != nil {
        log.Fatalf("Failed to acquire mutex: %v", err)
    }

    // Perform operations under lock
    // ...

    // Release lock
    if ok, err := mutex.Unlock(); !ok || err != nil {
        log.Fatalf("Failed to release mutex: %v", err)
    }
}
```
---

## Mocking support

Use MockZCache and MockZMutex for unit testing.

```go
func TestCacheOperation(t *testing.T) {
    mockCache := new(zcache.MockZCache)
    mockCache.On("Get", mock.Anything, "key1").Return("value1", nil)
    // Use mockCache in your tests
}

func TestSomeFunctionWithMutex(t *testing.T) {
    mockMutex := new(zcache.MockZMutex)
    mockMutex.On("Lock").Return(nil)
    mockMutex.On("Unlock").Return(true, nil)
    mockMutex.On("Name").Return("myMutex")
    
    result, err := SomeFunctionThatUsesMutex(mockMutex)
    assert.NoError(t, err)
    assert.Equal(t, expectedResult, result)
    
    mockMutex.AssertExpectations(t)
}
```

## Best Practices - Ristretto Cache

### Memory Management
When using the local cache (Ristretto), memory is managed efficiently:

1. **Memory Control**:
   - Ristretto uses precise memory tracking with a cost-based system
   - Items are evicted based on cost, access frequency, and recency
   - Built-in admission policy prevents low-value items from entering the cache

2. **Configuration Parameters**:
   - `NumCounters`: Number of keys to track (default: 1e7 or 10 million)
   - `MaxCostMB`: Maximum memory in MB (default: 1024MB or 1GB)
   - `BufferItems`: Size of the Get buffer for handling concurrent operations (default: 64)

3. **TTL Behavior**:
   - TTL is handled internally by Ristretto
   - Setting `ttl <= 0` in the API means the item never expires
   - Ristretto automatically removes expired items

### Memory Monitoring
Monitor cache performance through Ristretto metrics. The following metrics are available:

- `localCacheHitsMetricName`: Number of cache hits
- `localCacheMissesMetricName`: Number of cache misses
- `localCacheDelHitsMetricName`: Number of successful deletions
- `localCacheDelMissesMetricName`: Number of failed deletions
- `localCacheCollisionsMetricName`: Number of key collisions

For Redis metrics:
- `remoteCachePoolHitsMetricName`: Free connection found in the pool
- `remoteCachePoolMissesMetricName`: Free connection not found in the pool
- `remoteCachePoolTimeoutsMetricName`: Wait timeout occurrences
- `remoteCachePoolTotalConnsMetricName`: Total connections in the pool
- `remoteCachePoolIdleConnsMetricName`: Idle connections in the pool
- `remoteCachePoolStaleConnsMetricName`: Stale connections removed

### Best Practices
1. **Memory Configuration**:
   - Set appropriate `NumCounters` based on expected number of keys (~10x the items)
   - Configure `MaxCostMB` based on available system memory (in megabytes)
   - Adjust `BufferItems` for high concurrency scenarios (default 64 is suitable for most cases)

2. **Production Recommendations**:
   - Use Combined Cache with Redis for persistence
   - Monitor hit ratios through the exposed metrics
   - Set appropriate TTLs for data freshness
   - For critical production systems, implement fallback mechanisms

3. **Cache Tuning**:
   - For high-throughput systems, increase `BufferItems` 
   - For memory-constrained environments, decrease `MaxCostMB` appropriately
   - Keep `NumCounters` at approximately 10x your expected item count for optimal hit ratio

### Notes
- Ristretto provides better memory management than the previously used BigCache
- No known memory leak issues or required manual cleanup processes
- Predictable memory usage with automatic item eviction
- Better performance under high load with concurrent operations
- Cost-based eviction allows prioritizing important cache items 