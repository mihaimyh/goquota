# goquota

[![Go Reference](https://pkg.go.dev/badge/github.com/mihaimyh/goquota.svg)](https://pkg.go.dev/github.com/mihaimyh/goquota)
[![Go Report Card](https://goreportcard.com/badge/github.com/mihaimyh/goquota)](https://goreportcard.com/report/github.com/mihaimyh/goquota)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Subscription quota management for Go with anniversary-based billing cycles, prorated tier changes, and pluggable storage.

## Features

- **Anniversary-based billing cycles** - Preserve subscription anniversary dates across months
- **Prorated quota adjustments** - Handle mid-cycle tier changes fairly
- **Multiple quota types** - Support both daily and monthly quotas
- **Pluggable storage** - Redis (recommended), Firestore, In-Memory, or custom backends
- **High Performance** - Redis adapter uses atomic Lua scripts for <1ms latency
- **Transaction-safe** - Prevent over-consumption with atomic operations
- **Idempotency Keys** - Prevent double-charging on retries with client-provided idempotency keys
- **Refund Support** - Gracefully handle failed operations with idempotency and audit trails
- **Soft Limits & Warnings** - Trigger callbacks when usage approaches limits (e.g. 80%)
- **Fallback Strategies** - Graceful degradation when storage is unavailable (cache, optimistic, secondary storage)
- **Observability** - Built-in Prometheus metrics and structured logging
- **HTTP Middlewares** - Easy integration with standard `net/http` servers

## Installation

```bash
go get github.com/mihaimyh/goquota
```

## Quick Start

```go
package main

import (
    "context"
    "time"

    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

func main() {
    // Create storage
    storage := memory.New()

    // Configure tiers
    config := goquota.Config{
        DefaultTier: "free",
        Tiers: map[string]goquota.TierConfig{
            "free": {
                MonthlyQuotas: map[string]int{"api_calls": 100},
            },
            "pro": {
                MonthlyQuotas: map[string]int{"api_calls": 10000},
            },
        },
    }

    // Create manager
    manager, _ := goquota.NewManager(storage, &config)

    // Set user entitlement
    ctx := context.Background()
    manager.SetEntitlement(ctx, &goquota.Entitlement{
        UserID: "user123",
        Tier:   "pro",
        SubscriptionStartDate: time.Now().UTC(),
    })

    // Consume quota
    newUsed, err := manager.Consume(ctx, "user123", "api_calls", 1, goquota.PeriodTypeMonthly)
    if err == goquota.ErrQuotaExceeded {
        // Handle quota exceeded
    }
    
    // Consume with idempotency key (prevents double-charging on retries)
    newUsed, err = manager.Consume(
        ctx, 
        "user123", 
        "api_calls", 
        1, 
        goquota.PeriodTypeMonthly,
        goquota.WithIdempotencyKey("unique-request-id-123"),
    )
}
```

## Storage Adapters

### Redis (Recommended)

High-performance, atomic storage using Lua scripts. Supports clusters and automatic TTL expiration.

```go
import (
    "github.com/redis/go-redis/v9"
    redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

// Create Redis client
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

// Initialize storage
storage, err := redisStorage.New(client, redisStorage.Config{
    KeyPrefix:      "quota:",
    EntitlementTTL: 24 * time.Hour,
    UsageTTL:       7 * 24 * time.Hour, // Keep usage for 1 week past expiry
})
```

### Firestore (Serverless)

Ideal for Google Cloud Serverless environments.

```go
import (
    "cloud.google.com/go/firestore"
    firestoreStorage "github.com/mihaimyh/goquota/storage/firestore"
)

client, _ := firestore.NewClient(ctx, "project-id")
storage, _ := firestoreStorage.New(client, firestoreStorage.Config{
    EntitlementsCollection: "billing_entitlements",
    UsageCollection:        "billing_usage",
})
```

### In-Memory (Testing)

```go
import "github.com/mihaimyh/goquota/storage/memory"

storage := memory.New()
```

## Advanced Features

### Idempotency Keys

Prevent double-charging when clients retry failed requests by providing idempotency keys to `Consume` operations.

```go
// First request with idempotency key
newUsed, err := manager.Consume(
    ctx, 
    "user123", 
    "api_calls", 
    1, 
    goquota.PeriodTypeMonthly,
    goquota.WithIdempotencyKey("req_abc123"), // Unique key for this operation
)
if err != nil {
    // Request failed, client retries...
}

// Retry with same idempotency key - returns cached result, no double-charge
newUsed, err = manager.Consume(
    ctx, 
    "user123", 
    "api_calls", 
    1, 
    goquota.PeriodTypeMonthly,
    goquota.WithIdempotencyKey("req_abc123"), // Same key
)
// Returns the same newUsed value from first request, quota only consumed once
```

Idempotency keys are automatically deduplicated across all storage backends and have a configurable TTL (default: 24 hours).

### Quota Refunds

Handle failed operations by refunding the consumed quota. Supports idempotency keys to prevent double-refunds.

```go
// Operation failed, refund the quota
err := manager.Refund(ctx, &goquota.RefundRequest{
    UserID:         "user123",
    Resource:       "api_calls",
    Amount:         1,
    PeriodType:     goquota.PeriodTypeMonthly,
    IdempotencyKey: "req_123_refund", // Unique key for this refund
    Reason:         "Service timeout",
})
```

### Soft Limits & Warnings

Receive notifications when a user is nearing their limit.

```go
manager.SetWarningCallback(func(ctx context.Context, userID, resource string, pctUsed float64) {
    if pctUsed >= 80.0 {
        fmt.Printf("Warning: User %s used %.2f%% of %s quota\n", userID, pctUsed, resource)
        // Send email alert, etc.
    }
})
```

### Fallback Strategies

Enable graceful degradation when storage is unavailable. Supports multiple fallback strategies that can be combined.

```go
import (
    "time"
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

// Configure fallback strategies
config := goquota.Config{
    // ... other config ...
    CacheConfig: &goquota.CacheConfig{
        Enabled: true,
        EntitlementTTL: 5 * time.Minute,
        UsageTTL: 30 * time.Second,
    },
    FallbackConfig: &goquota.FallbackConfig{
        Enabled:                    true,
        FallbackToCache:            true,  // Use cached data when storage fails
        OptimisticAllowance:        true,  // Allow optimistic consumption
        OptimisticAllowancePercentage: 10.0, // Up to 10% of quota
        SecondaryStorage:           secondaryStorage, // Optional secondary storage
        MaxStaleness:               5 * time.Minute, // Max cache age
    },
}

manager, _ := goquota.NewManager(primaryStorage, &config)
```

**Available Strategies:**
- **Cache Fallback**: Falls back to cached data when storage fails (validates staleness)
- **Optimistic Allowance**: Allows quota consumption optimistically up to a configurable percentage
- **Secondary Storage**: Falls back to a secondary storage backend (works with any Storage implementation)

Fallback strategies are tried in order when storage failures occur, enabling continued operation during outages.

### Metrics

The library exposes Prometheus metrics by default via the `metrics` package.

- `goquota_ops_total{operation="consume", status="success"}`
- `goquota_ops_latency_seconds`
- `goquota_usage_ratio`
- `goquota_fallback_usage_total{trigger="circuit_open"}`
- `goquota_optimistic_consumption_total`
- `goquota_fallback_hits_total{strategy="cache"}`

## HTTP Middleware

Integrate directly with your HTTP handlers.

```go
import (
    "net/http"
    httpMiddleware "github.com/mihaimyh/goquota/middleware/http"
)

// Create middleware
quotaMiddleware := httpMiddleware.Middleware(httpMiddleware.Config{
    Manager:     manager,
    GetUserID:   httpMiddleware.FromHeader("X-User-ID"),
    GetResource: httpMiddleware.FixedResource("api_calls"),
    GetAmount:   httpMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeDaily,
    // Optional: Only blocking if over 100% of limit, but warn at 80%
    UseSoftLimit: false,
})

// Apply to handler
http.Handle("/api/endpoint", quotaMiddleware(yourHandler))
```

## Anniversary-Based Billing & Proration

`goquota` is designed for real-world subscription billing:

- **Anniversaries**: Cycles follow the user's subscription date (e.g. Jan 15 -> Feb 15), not calendar months.
- **Proration**: If a user upgrades headers mid-cycle, their usage carries over proportionally.

  > _Example_: User consumes 50% of Basic tier. Upgrading to Pro (10x limit) means they start with 50% of Pro usage, preserving the "percent used" fairness.

## Examples

See the [examples](examples/) directory:

- [Basic Usage](examples/basic/)
- [Redis Integration](examples/redis/)
- [Firestore Integration](examples/firestore/)
- [HTTP Server](examples/http-server/)
- [Fallback Strategies](examples/fallback/)

## API Reference

### Manager Interface

```go
// Core Operations
Consume(ctx, userID, resource, amount, periodType, opts ...ConsumeOption) (int, error)
Refund(ctx, req *RefundRequest) error
GetQuota(ctx, userID, resource, periodType) (*Usage, error)

// Management
SetEntitlement(ctx, entitlement) error
ApplyTierChange(ctx, userID, oldTier, newTier, resource) error
SetWarningCallback(callback)
```

## Testing

```bash
# Run all tests with coverage
go test -cover ./...
```

## License

MIT License - see [LICENSE](LICENSE) for details
