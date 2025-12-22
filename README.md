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
- **Rate Limiting** - Time-based request frequency limits (requests per second/minute/hour) with token bucket and sliding window algorithms
- **Soft Limits & Warnings** - Trigger callbacks when usage approaches limits (e.g. 80%)
- **Fallback Strategies** - Graceful degradation when storage is unavailable (cache, optimistic, secondary storage)
- **Observability** - Built-in Prometheus metrics and structured logging
- **HTTP Middlewares** - Easy integration with standard `net/http` servers and Gin framework with rate limit headers

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

    // Configure tiers with rate limits
    config := goquota.Config{
        DefaultTier: "free",
        Tiers: map[string]goquota.TierConfig{
            "free": {
                MonthlyQuotas: map[string]int{"api_calls": 100},
                // Rate limits: 10 requests per second with burst of 20
                RateLimits: map[string]goquota.RateLimitConfig{
                    "api_calls": {
                        Algorithm: "token_bucket",
                        Rate:      10,
                        Window:    time.Second,
                        Burst:     20,
                    },
                },
            },
            "pro": {
                MonthlyQuotas: map[string]int{"api_calls": 10000},
                // Rate limits: 100 requests per second (sliding window)
                RateLimits: map[string]goquota.RateLimitConfig{
                    "api_calls": {
                        Algorithm: "sliding_window",
                        Rate:      100,
                        Window:    time.Second,
                    },
                },
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

**⚠️ Firestore Infrastructure Requirements:**

1. **TTL Policy Configuration (Required)**
   - The library adds an `expiresAt` field to consumption and refund documents for automatic cleanup
   - **You must configure a Time-to-Live (TTL) policy** in Google Cloud Console or via Terraform for the `consumptions` and `refunds` collections targeting the `expiresAt` field
   - Without TTL policies, documents will accumulate indefinitely despite the field presence
   - Configure TTL policies:
     - **Google Cloud Console**: Navigate to Firestore → Data → Select collection → Enable TTL → Choose `expiresAt` field
     - **Terraform**: Use `google_firestore_field` resource with `ttl_config` block
   - Example Terraform configuration:
     ```hcl
     resource "google_firestore_field" "consumptions_ttl" {
       project     = "your-project-id"
       database    = "(default)"
       collection  = "billing_consumptions"
       field       = "expiresAt"
       ttl_config  {}
     }
     ```

2. **Composite Indexes (If Querying by expiresAt)**
   - If you plan to query documents by `expiresAt` (e.g., for manual cleanup jobs or debugging), Firestore requires composite indexes when filtering by multiple fields
   - The library uses direct document lookups by ID (idempotency key), so indexes are not required for normal operation
   - If you add custom cleanup functions that query `WHERE expiresAt < NOW()`, ensure you create the required composite indexes in Firestore

### In-Memory (Testing)

```go
import "github.com/mihaimyh/goquota/storage/memory"

storage := memory.New()
```

## Advanced Features

### Rate Limiting

Enforce time-based request frequency limits in addition to quota limits. Rate limiting prevents API abuse and protects backend services.

**Token Bucket Algorithm** - Allows burst traffic with configurable refill rate:
```go
RateLimits: map[string]goquota.RateLimitConfig{
    "api_calls": {
        Algorithm: "token_bucket",
        Rate:      10,              // 10 requests per window
        Window:    time.Second,      // 1 second window
        Burst:     20,              // Allow up to 20 requests in burst
    },
}
```

**Sliding Window Algorithm** - More precise rate limiting:
```go
RateLimits: map[string]goquota.RateLimitConfig{
    "api_calls": {
        Algorithm: "sliding_window",
        Rate:      100,             // 100 requests per window
        Window:    time.Minute,     // 1 minute window
    },
}
```

Rate limits are checked **before** quota consumption, so rate-limited requests don't consume quota. When a rate limit is exceeded, the system returns `ErrRateLimitExceeded` with reset time information.

**HTTP Middleware Integration** - Rate limit headers are automatically added:
- `X-RateLimit-Limit`: Total rate limit
- `X-RateLimit-Remaining`: Remaining requests in current window
- `X-RateLimit-Reset`: Unix timestamp when the limit resets
- `Retry-After`: Seconds until the limit resets

**Distributed Rate Limiting** - Rate limits work across multiple instances using Redis or Firestore storage backends, ensuring consistent enforcement in distributed systems.

**Graceful Degradation** - If storage is unavailable for rate limiting, requests are allowed (with logging) to prevent rate limiting from blocking legitimate requests during outages.

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

**⚠️ Multi-Instance Deployment Warning:**
When deploying multiple instances of your application with fallback strategies enabled, be aware that:
- **Cache Fallback** uses per-instance in-memory caches. Each instance maintains its own cache, which can lead to temporary inconsistencies across instances during storage outages.
- **Optimistic Allowance** tracks consumption per-instance. In a deployment with N instances, the total optimistic consumption across all instances could theoretically approach N × configured percentage (e.g., 5 instances × 10% = 50% of quota). Monitor `goquota_optimistic_consumption_total` metrics across all instances to track total optimistic usage.
- **Recommended Practices:**
  - Use optimistic allowance percentages conservatively (5-10%) in multi-instance deployments
  - Monitor aggregate optimistic consumption metrics across all instances
  - Prefer Redis storage (with high availability) over fallback strategies for production workloads
  - Consider using secondary storage fallback (e.g., Firestore) instead of optimistic allowance for better consistency

### Metrics

The library exposes Prometheus metrics by default via the `metrics` package.

- `goquota_ops_total{operation="consume", status="success"}`
- `goquota_ops_latency_seconds`
- `goquota_usage_ratio`
- `goquota_fallback_usage_total{trigger="circuit_open"}`
- `goquota_optimistic_consumption_total`
- `goquota_fallback_hits_total{strategy="cache"}`
- `goquota_rate_limit_check_duration_seconds{resource="api_calls"}`
- `goquota_rate_limit_exceeded_total{resource="api_calls"}`

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

The middleware automatically handles both quota limits and rate limits. When a rate limit is exceeded, it returns `429 Too Many Requests` with appropriate headers.

### Gin Framework Middleware

Native middleware for the [Gin](https://github.com/gin-gonic/gin) web framework.

```go
import (
    "github.com/gin-gonic/gin"
    ginMiddleware "github.com/mihaimyh/goquota/middleware/gin"
)

// Create Gin router
r := gin.Default()

// Mock authentication middleware (sets UserID in context)
r.Use(func(c *gin.Context) {
    userID := c.GetHeader("X-User-ID")
    if userID != "" {
        c.Set("UserID", userID) // Set in context for quota middleware
    }
    c.Next()
})

// Apply quota middleware
api := r.Group("/api")
api.Use(ginMiddleware.Middleware(ginMiddleware.Config{
    Manager:     manager,
    GetUserID:   ginMiddleware.FromContext("UserID"), // Recommended: Extract from context (set by auth middleware)
    GetResource: ginMiddleware.FixedResource("api_calls"),
    GetAmount:   ginMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeDaily,
}))

api.GET("/data", func(c *gin.Context) {
    c.String(200, "Data retrieved successfully")
})
```

**Framework-Specific Extractors:**
- `FromContext(key)` - Extract from Gin context (recommended for auth middleware integration)
- `FromHeader(headerName)` - Extract from HTTP header
- `FromParam(paramName)` - Extract from route parameter
- `FromQuery(queryName)` - Extract from query parameter

**Custom Error Responses:**
The middleware supports callback-based error handling for complete customization:

```go
api.Use(ginMiddleware.Middleware(ginMiddleware.Config{
    Manager:     manager,
    GetUserID:   ginMiddleware.FromContext("UserID"),
    GetResource: ginMiddleware.FixedResource("api_calls"),
    GetAmount:   ginMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeDaily,
    // Custom error responses
    OnQuotaExceeded: func(c *gin.Context, usage *goquota.Usage) {
        c.JSON(http.StatusPaymentRequired, gin.H{
            "error": gin.H{
                "code":    "QUOTA_EXCEEDED",
                "message": "Monthly quota exceeded",
                "details": gin.H{
                    "used":  usage.Used,
                    "limit": usage.Limit,
                },
            },
        })
    },
    OnRateLimitExceeded: func(c *gin.Context, retryAfter time.Duration, info *goquota.RateLimitInfo) {
        c.JSON(http.StatusTooManyRequests, gin.H{
            "error":      "Rate limit exceeded",
            "retry_after": retryAfter.Seconds(),
        })
    },
}))
```

**Dynamic Cost Calculation:**
Calculate quota consumption based on request type:

```go
api.Use(ginMiddleware.Middleware(ginMiddleware.Config{
    Manager:     manager,
    GetUserID:   ginMiddleware.FromContext("UserID"),
    GetResource: ginMiddleware.FixedResource("api_calls"),
    GetAmount: ginMiddleware.DynamicCost(func(c *gin.Context) int {
        // POST requests cost 5, GET requests cost 1
        if c.Request.Method == "POST" {
            return 5
        }
        return 1
    }),
    PeriodType: goquota.PeriodTypeDaily,
}))
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
- [Gin Framework](examples/gin/)
- [Fallback Strategies](examples/fallback/)
- [Rate Limiting](examples/rate-limiting/)

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
