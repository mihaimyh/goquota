# Tiered Storage Adapter

The Tiered Storage Adapter provides a Hot/Cold storage architecture that combines the speed of fast ephemeral storage (Hot) with the durability of persistent storage (Cold). This adapter implements the Decorator Pattern, allowing you to use any combination of storage backends without modifying your Manager code.

## Features

- **Performance**: 99% of traffic hits Hot store (Redis) for sub-millisecond latency
- **Durability**: Critical data persisted in Cold store (Postgres/Firestore) as source of truth
- **Resilience**: Automatic cache repopulation from Cold on Hot miss/failure
- **Flexibility**: Works with any `goquota.Storage` implementation
- **No Code Changes**: Pure decorator pattern - no changes to Manager or core logic required

## Architecture

The tiered storage adapter orchestrates two storage backends:

- **Hot (L1)**: Fast, ephemeral storage (Redis, Memory) for high-frequency operations
- **Cold (L2)**: Durable, persistent storage (Postgres, Firestore) as the source of truth

Different operations use different data strategies optimized for their requirements:

| Operation | Strategy | Implementation |
|-----------|----------|----------------|
| **Entitlements** | Read-Through / Write-Through | Read Hot → (miss) → Read Cold → Populate Hot<br/>Write Cold → (success) → Write Hot |
| **Rate Limits** | Hot-Only | All operations on Hot only |
| **Quota Consumption** | Hot-Primary / Async-Audit | Consume on Hot (atomic)<br/>Async flush to Cold for audit |
| **Refunds** | Write-Through | Write Cold → Write Hot |
| **Usage Reads** | Read-Through | Read Hot → (miss) → Read Cold |
| **Usage Writes** | Write-Through | Write Cold → Write Hot |
| **Tier Changes** | Write-Through | Write Cold → Write Hot |
| **Add/Subtract Limit** | Write-Through | Write Cold → Write Hot |
| **GetConsumptionRecord** | Read-Through | Read Hot → Cold (Critical for idempotency) |
| **GetRefundRecord** | Read-Through | Read Hot → Cold |

## Installation

```bash
go get github.com/mihaimyh/goquota/storage/tiered
```

## Usage

### Redis + PostgreSQL Example

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/redis/go-redis/v9"
    
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/postgres"
    "github.com/mihaimyh/goquota/storage/redis"
    "github.com/mihaimyh/goquota/storage/tiered"
)

func main() {
    ctx := context.Background()
    
    // 1. Initialize Hot Store (Redis)
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    hotStore, err := redis.New(redisClient, redis.DefaultConfig())
    if err != nil {
        log.Fatal(err)
    }
    
    // 2. Initialize Cold Store (PostgreSQL)
    pgConfig := postgres.DefaultConfig()
    pgConfig.ConnectionString = "postgres://user:password@localhost:5432/goquota?sslmode=disable"
    coldStore, err := postgres.New(ctx, pgConfig)
    if err != nil {
        log.Fatal(err)
    }
    defer coldStore.Close()
    
    // 3. Initialize Tiered Storage
    tieredStore, err := tiered.New(tiered.Config{
        Hot:            hotStore,
        Cold:           coldStore,
        AsyncUsageSync: true, // Non-blocking Postgres writes for consumption
        AsyncErrorHandler: func(err error) {
            log.Printf("Background sync failed: %v", err)
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer tieredStore.Close()
    
    // 4. Create Manager with Tiered Storage
    managerConfig := &goquota.Config{
        Tiers: map[string]goquota.TierConfig{
            "pro": {
                Name: "pro",
                MonthlyQuotas: map[string]int{
                    "api_calls": 10000,
                },
            },
        },
        DefaultTier: "pro",
    }
    
    manager, err := goquota.NewManager(tieredStore, managerConfig)
    if err != nil {
        log.Fatal(err)
    }
    
    // Now:
    // - Rate limits hit Redis only (Hot-Only)
    // - Entitlements read from Redis (fallback to PostgreSQL)
    // - Consumption hits Redis immediately, updates PostgreSQL in background
    
    _, err = manager.Consume(ctx, "user123", "api_calls", 10, goquota.PeriodTypeMonthly)
    if err != nil {
        log.Fatal(err)
    }
}
```

### Redis + Firestore Example

```go
package main

import (
    "context"
    "log"
    
    "cloud.google.com/go/firestore"
    "github.com/redis/go-redis/v9"
    
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/firestore"
    "github.com/mihaimyh/goquota/storage/redis"
    "github.com/mihaimyh/goquota/storage/tiered"
)

func main() {
    ctx := context.Background()
    
    // 1. Initialize Hot Store (Redis)
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    hotStore, err := redis.New(redisClient, redis.DefaultConfig())
    if err != nil {
        log.Fatal(err)
    }
    
    // 2. Initialize Cold Store (Firestore)
    fsClient, err := firestore.NewClient(ctx, "your-project-id")
    if err != nil {
        log.Fatal(err)
    }
    coldStore, err := firestore.New(fsClient, firestore.Config{})
    if err != nil {
        log.Fatal(err)
    }
    defer fsClient.Close()
    
    // 3. Initialize Tiered Storage
    tieredStore, err := tiered.New(tiered.Config{
        Hot:            hotStore,
        Cold:           coldStore,
        AsyncUsageSync: true,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer tieredStore.Close()
    
    // 4. Use with Manager...
}
```

## Configuration

```go
type Config struct {
    // Hot is the L1 cache storage (e.g., Redis, Memory) for high-frequency operations
    Hot goquota.Storage
    
    // Cold is the L2 persistence storage (e.g., Postgres, Firestore) as the source of truth
    Cold goquota.Storage
    
    // AsyncUsageSync enables non-blocking synchronization for high-frequency
    // operations (ConsumeQuota). If false, writes are synchronous (slower but safer).
    AsyncUsageSync bool
    
    // SyncBufferSize is the size of the buffered channel for async operations.
    // Default: 1000
    SyncBufferSize int
    
    // AsyncErrorHandler is called when an async operation fails.
    // Essential for monitoring consistency drift.
    AsyncErrorHandler func(error)
}
```

## Data Strategies Explained

### Read-Through (Entitlements, Usage Reads, Record Retrieval)

Read-Through strategy checks Hot store first, then falls back to Cold store if not found. The result is then populated in Hot store for future reads (read-repair).

**Example: GetEntitlement**
1. Try Hot store
2. If miss, try Cold store (source of truth)
3. Populate Hot store with result
4. Return result

**Benefits:**
- Fast reads from Hot store (cache hit)
- Durable source of truth in Cold store
- Automatic cache warming on miss

### Write-Through (Entitlements, Usage Writes, Tier Changes, Limits, Refunds)

Write-Through strategy writes to Cold store first (for durability), then writes to Hot store (for availability). If Hot store write fails, the operation still succeeds because Cold store is the source of truth.

**Example: SetEntitlement**
1. Write to Cold store (durability guarantee)
2. Write to Hot store (best effort - errors are ignored)

**Benefits:**
- Durability: Critical data persisted in Cold store first
- Availability: Hot store updated for fast subsequent reads
- Resilience: Hot store failures don't affect durability

### Hot-Only (Rate Limits)

Rate limiting operations use Hot store only. This is necessary because rate limits require extreme speed (1000+ RPS) and are ephemeral by nature.

**Example: CheckRateLimit**
- All operations on Hot store only
- Cold store is never accessed

**Benefits:**
- Maximum performance (sub-millisecond latency)
- Suitable for high-frequency operations
- Ephemeral data doesn't need durability

### Hot-Primary / Async-Audit (Quota Consumption)

Quota consumption uses Hot store for immediate enforcement (atomic operation), then asynchronously syncs to Cold store for audit trail. This provides the best of both worlds: speed and durability.

**Example: ConsumeQuota**
1. Consume quota on Hot store (atomic, fast)
2. Queue async sync to Cold store (non-blocking)
3. Return immediately to user

**Benefits:**
- Fast user-facing operations (Hot store latency)
- Durable audit trail (Cold store)
- Non-blocking: Cold store failures don't block users

**Critical Note:** `GetConsumptionRecord` uses Read-Through (Hot → Cold) to ensure idempotency checks work correctly during the async sync lag window. If a client retries immediately after a network timeout, the record will be found in Hot store even if it hasn't synced to Cold yet.

## Performance Considerations

### Typical Performance Characteristics

- **Hot Store Hit (Redis)**: < 1ms latency
- **Cold Store Hit (PostgreSQL)**: 5-20ms latency
- **Async Sync**: Non-blocking, background processing

### Async Worker

The async worker processes sync operations sequentially to maintain causal ordering per user. This ensures that operations are applied in the correct order for consistency.

- **Sequential Processing**: One item at a time (per user ordering)
- **Buffered Channel**: Default 1000 items (configurable)
- **Queue Full Protection**: Non-blocking when queue is full (drops with error handler)
- **Graceful Shutdown**: Drains queue on Close()

### Monitoring Async Errors

Always provide an `AsyncErrorHandler` to monitor consistency drift:

```go
tieredStore, err := tiered.New(tiered.Config{
    Hot:            hotStore,
    Cold:           coldStore,
    AsyncUsageSync: true,
    AsyncErrorHandler: func(err error) {
        // Log to monitoring system (e.g., Sentry, Datadog)
        metrics.IncCounter("tiered.async_sync_errors")
        logger.Error("tiered storage async sync failed", zap.Error(err))
    },
})
```

## Error Handling

### Read-Through Errors

- Hot store errors fall back to Cold store
- Cold store errors are propagated to caller
- Hot store population errors are ignored (best effort)

### Write-Through Errors

- Cold store errors fail the operation (source of truth)
- Hot store errors are ignored (best effort - Cold succeeded)

### Async Operations

- Hot store errors are propagated to caller (immediate failure)
- Cold store sync errors call `AsyncErrorHandler` but don't block user
- Queue full scenarios call `AsyncErrorHandler` and drop the operation

## TimeSource Support

The tiered storage adapter delegates `TimeSource` to Hot store if supported, falling back to Cold store, then local time:

```go
// Prefer Hot store time (Redis TIME command)
if ts, ok := s.hot.(goquota.TimeSource); ok {
    return ts.Now(ctx)
}
// Fallback to Cold if Hot doesn't support it
if ts, ok := s.cold.(goquota.TimeSource); ok {
    return ts.Now(ctx)
}
// Final fallback to local time
return time.Now().UTC(), nil
```

This ensures consistent time across distributed systems when using Redis as Hot store.

## Graceful Shutdown

Always call `Close()` when shutting down to gracefully drain the async queue:

```go
defer tieredStore.Close()
```

The `Close()` method:
1. Signals the async worker to stop
2. Drains the sync queue (best effort)
3. Waits for worker to finish
4. Is safe to call multiple times (idempotent)

## Use Cases

### High-Traffic API

- **Hot Store**: Redis (sub-millisecond rate limiting)
- **Cold Store**: PostgreSQL (durable billing records)
- **Strategy**: Async consumption sync for minimal latency

### Multi-Region Deployment

- **Hot Store**: Redis Cluster (local to region)
- **Cold Store**: PostgreSQL (global source of truth)
- **Strategy**: Read-through ensures cache consistency

### Cost Optimization

- **Hot Store**: Memory storage (in-process, fast)
- **Cold Store**: Firestore (serverless, pay-per-use)
- **Strategy**: Minimal Cold store reads (cache-first)

## Limitations

1. **Async Consistency**: Consumption records may take a few milliseconds to sync to Cold store. Use `GetConsumptionRecord` with Read-Through strategy to handle idempotency correctly.

2. **Hot Store Failure**: If Hot store fails, operations will fall back to Cold store (slower but functional). Rate limits will fail completely (Hot-Only strategy).

3. **Queue Full**: If the async queue is full, Cold store sync operations are dropped. Monitor via `AsyncErrorHandler`.

## Migration from Single Storage

You can migrate from a single storage backend to tiered storage without changing your Manager code:

```go
// Before (Redis only)
storage, _ := redis.New(client, redis.DefaultConfig())
manager, _ := goquota.NewManager(storage, config)

// After (Redis + PostgreSQL tiered)
hotStore, _ := redis.New(client, redis.DefaultConfig())
coldStore, _ := postgres.New(ctx, pgConfig)
tieredStore, _ := tiered.New(tiered.Config{Hot: hotStore, Cold: coldStore})
manager, _ := goquota.NewManager(tieredStore, config) // Same Manager code!
```

## Testing

The tiered storage adapter includes comprehensive tests. Run tests with:

```bash
go test ./storage/tiered/... -v
```

Tests use in-memory storage for Hot and Cold stores to avoid external dependencies.

## License

Same as the main goquota library.

