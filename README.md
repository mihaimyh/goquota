# goquota

[![Go Reference](https://pkg.go.dev/badge/github.com/mihaimyh/goquota.svg)](https://pkg.go.dev/github.com/mihaimyh/goquota)
[![Go Report Card](https://goreportcard.com/badge/github.com/mihaimyh/goquota)](https://goreportcard.com/report/github.com/mihaimyh/goquota)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Subscription quota management for Go with anniversary-based billing cycles, prorated tier changes, and pluggable storage.

## Features

- **Anniversary-based billing cycles** - Preserve subscription anniversary dates across months
- **Prorated quota adjustments** - Handle mid-cycle tier changes fairly
- **Multiple quota types** - Support both daily and monthly quotas
- **Pluggable storage** - Redis (recommended), PostgreSQL, Firestore, In-Memory, or custom backends
- **Tiered Storage** - Hot/Cold architecture combining Redis speed with PostgreSQL/Firestore durability
- **High Performance** - Redis adapter uses atomic Lua scripts for <1ms latency
- **Transaction-safe** - Prevent over-consumption with atomic operations
- **Idempotency Keys** - Prevent double-charging on retries with client-provided idempotency keys
- **Refund Support** - Gracefully handle failed operations with idempotency and audit trails
- **Rate Limiting** - Time-based request frequency limits (requests per second/minute/hour) with token bucket and sliding window algorithms
- **Soft Limits & Warnings** - Trigger callbacks when usage approaches limits (e.g. 80%)
- **Admin Operations** - Manual quota management for incident response (SetUsage, GrantOneTimeCredit, ResetUsage)
- **Dry-Run Mode** - Test quota rules without blocking traffic for safe deployments
- **Audit Trail** - Comprehensive logging of all quota changes for compliance and debugging
- **Clock Skew Protection** - Uses storage server time to prevent quota double-spending at reset boundaries
- **Enhanced Response** - Get detailed usage info without extra storage calls (50% Redis load reduction)
- **Config Validation** - Fail fast on startup with comprehensive configuration validation
- **Fallback Strategies** - Graceful degradation when storage is unavailable (cache, optimistic, secondary storage)
- **Observability** - Built-in Prometheus metrics and structured logging
- **HTTP Middlewares** - Easy integration with standard `net/http` servers, Gin, Echo, and Fiber frameworks with rate limit headers
- **Billing Provider Integration** - Unified interface for RevenueCat, Stripe, and other payment providers with automatic webhook processing
- **Standardized Usage API** - Pre-built HTTP endpoints for exposing quota state to frontend applications with unified monthly + forever credits view

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

### PostgreSQL (SQL-based)

Ideal for applications already using PostgreSQL. Provides ACID transactions with row-level locking for atomic quota operations. Rate limiting is handled in-memory for performance.

```go
import (
    "context"
    "time"

    postgresStorage "github.com/mihaimyh/goquota/storage/postgres"
)

ctx := context.Background()
config := postgresStorage.DefaultConfig()
config.ConnectionString = "postgres://user:password@localhost:5432/goquota?sslmode=disable"
config.CleanupEnabled = true
config.CleanupInterval = 1 * time.Hour
config.RecordTTL = 7 * 24 * time.Hour

storage, err := postgresStorage.New(ctx, config)
if err != nil {
    log.Fatal(err)
}
defer storage.Close() // Important: closes connection pool and cleanup goroutine
```

**Database Setup:**
Run the migration to create required tables:

```bash
psql -d goquota -f storage/postgres/migrations/001_initial_schema.sql
```

**Important Notes:**

- **Quotas**: Stored in PostgreSQL with global synchronization across instances
- **Rate Limits**: Handled in-memory (local per instance) for performance. In a cluster of N instances, effective rate limit is `N × ConfiguredRate`
- **Idempotency Keys**: Scoped per user, allowing safe reuse across different users
- **Cleanup**: Automatic background cleanup of expired audit records

See [storage/postgres/README.md](storage/postgres/README.md) for detailed documentation.

### In-Memory (Testing)

```go
import "github.com/mihaimyh/goquota/storage/memory"

storage := memory.New()
```

### Tiered Storage (Hot/Cold Architecture)

Combine the speed of Redis with the durability of PostgreSQL/Firestore using a Hot/Cold tiered storage architecture. This adapter implements different strategies optimized for each operation type:

- **Read-Through**: Entitlements and usage reads check Hot first, fall back to Cold, then populate Hot
- **Write-Through**: Critical writes (entitlements, tier changes) write to Cold first, then Hot
- **Hot-Primary/Async-Audit**: Quota consumption writes to Hot immediately (atomic), then syncs to Cold asynchronously for audit trail
- **Hot-Only**: Rate limits operate on Hot only for maximum performance

**Benefits:**
- **Performance**: 99% of traffic hits Hot store for sub-millisecond latency
- **Durability**: Critical data persisted in Cold store as source of truth
- **Resilience**: Automatic cache repopulation from Cold on Hot miss/failure
- **No Code Changes**: Pure decorator pattern - works with any storage combination

```go
import (
    "github.com/mihaimyh/goquota/storage/redis"
    "github.com/mihaimyh/goquota/storage/postgres"
    "github.com/mihaimyh/goquota/storage/tiered"
)

// Initialize Hot (Redis) and Cold (PostgreSQL) stores
hotStore, _ := redis.New(redisClient, redis.DefaultConfig())
coldStore, _ := postgres.New(ctx, postgresConfig)

// Create tiered storage adapter
tieredStore, _ := tiered.New(tiered.Config{
    Hot:            hotStore,
    Cold:           coldStore,
    AsyncUsageSync: true, // Non-blocking Cold writes for consumption
    AsyncErrorHandler: func(err error) {
        log.Printf("Background sync failed: %v", err)
    },
})
defer tieredStore.Close()

// Use tiered storage with Manager (no other changes needed)
manager, _ := goquota.NewManager(tieredStore, &config)
```

See [storage/tiered/README.md](storage/tiered/README.md) for complete documentation.

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

### Pre-Paid Credits (Non-Expiring Resources)

`goquota` supports pre-paid credits that never expire until consumed, enabling hybrid billing models (subscriptions + credit packs) essential for AI/LLM SaaS applications.

#### Overview

Pre-paid credits are non-expiring quotas that persist until used. They're perfect for:

- **Credit Packs**: "Buy 500 Image Generations for $10"
- **Hybrid Billing**: Combine monthly subscriptions with one-time credit purchases
- **Sign-up Bonuses**: Give new users free credits when they sign up

#### Top-Up Credits

Add credits to a user's account atomically with transactional idempotency:

```go
// Top up credits (e.g., user purchased 100 credits)
err := manager.TopUpLimit(
    ctx,
    "user123",
    "api_calls",
    100, // Amount of credits to add
    goquota.WithTopUpIdempotencyKey("payment_intent_abc123"), // Prevent duplicate processing
)
if err != nil {
    // Handle error (e.g., invalid amount, storage error)
}
```

**Idempotency**: The same idempotency key can be used multiple times safely (e.g., webhook retries). The credits are added exactly once.

#### Refund Credits

Refund pre-paid credits when a payment is refunded:

```go
// Refund credits (e.g., payment was refunded)
err := manager.RefundCredits(
    ctx,
    "user123",
    "api_calls",
    50, // Amount of credits to refund
    "Payment refunded",
    goquota.WithRefundIdempotencyKey("refund_xyz789"), // Prevent duplicate processing
)
if err != nil {
    // Handle error
}
```

**Negative Limit Prevention**: The system automatically clamps limits to 0, preventing negative balances.

#### Forever Period Consumption

Consume from pre-paid credits using `PeriodTypeForever`:

```go
// Consume from forever credits
newUsed, err := manager.Consume(
    ctx,
    "user123",
    "api_calls",
    10,
    goquota.PeriodTypeForever, // Use forever credits
)
if err == goquota.ErrQuotaExceeded {
    // User has no forever credits or insufficient balance
}
```

#### Cascading Consumption (Hybrid Billing)

Automatically try multiple quota types in order (e.g., monthly subscription first, then forever credits):

```go
config := goquota.Config{
    Tiers: map[string]goquota.TierConfig{
        "pro": {
            MonthlyQuotas: map[string]int{
                "api_calls": 1000, // Monthly subscription quota
            },
            // Define consumption order: try monthly first, then forever credits
            ConsumptionOrder: []goquota.PeriodType{
                goquota.PeriodTypeMonthly,
                goquota.PeriodTypeForever,
            },
        },
    },
}

manager, _ := goquota.NewManager(storage, &config)

// Consume with auto-cascading: tries monthly first, falls back to forever credits
newUsed, err := manager.Consume(
    ctx,
    "user123",
    "api_calls",
    50,
    goquota.PeriodTypeAuto, // Automatic cascading consumption
)
```

**How it works**:

1. Tries to consume from `PeriodTypeMonthly` (subscription quota)
2. If monthly quota is exhausted, automatically tries `PeriodTypeForever` (pre-paid credits)
3. Returns `ErrQuotaExceeded` only if both are exhausted

This enables seamless hybrid billing without manual fallback logic in your application code.

#### Initial Forever Credits (Sign-up Bonuses)

Give new users free credits when they first get a tier:

```go
config := goquota.Config{
    Tiers: map[string]goquota.TierConfig{
        "pro": {
            MonthlyQuotas: map[string]int{
                "api_calls": 1000,
            },
            // Give 25 free credits when user first gets this tier
            InitialForeverCredits: map[string]int{
                "api_calls": 25,
            },
        },
    },
}

// When you set the entitlement, initial credits are automatically applied
err := manager.SetEntitlement(ctx, &goquota.Entitlement{
    UserID:                "user123",
    Tier:                  "pro",
    SubscriptionStartDate: time.Now().UTC(),
    UpdatedAt:             time.Now().UTC(),
})
// InitialForeverCredits are applied automatically (idempotent, safe for concurrent calls)
```

**Race Condition Safe**: Uses deterministic idempotency keys to ensure credits are applied exactly once, even with concurrent sign-up requests.

#### Stripe Integration for One-Time Payments

The Stripe billing provider supports one-time credit purchases:

```go
import (
    "github.com/mihaimyh/goquota/pkg/billing/stripe"
)

// Create Stripe provider
stripeProvider, _ := stripe.NewProvider(billing.Config{
    Manager: manager,
    Secret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
    // ... other config
})

// Create checkout URL for one-time payment (credit pack)
checkoutURL, err := stripeProvider.CheckoutURLForPayment(
    ctx,
    "user123",           // User ID
    "api_calls",         // Resource
    1000,                // Amount in cents (e.g., $10.00 = 1000 cents)
    "https://app.com/success", // Success URL
    "https://app.com/cancel",  // Cancel URL
)

// Redirect user to checkoutURL
```

**Webhook Processing**: The Stripe provider automatically:

- Processes `checkout.session.completed` events for one-time payments
- Calls `TopUpLimit()` with the payment amount
- Uses `payment_intent.id` as the idempotency key (prevents duplicate processing)
- Processes `payment_intent.refunded` events and calls `RefundCredits()`

**Credit Conversion**: By default, the system assumes 1 cent = 1 credit. For custom conversion rates (e.g., $10 for 500 credits), you can:

- Store the conversion rate in checkout session metadata
- Calculate credits in your webhook handler before calling `TopUpLimit()`

#### Example: Hybrid Billing Setup

```go
config := goquota.Config{
    Tiers: map[string]goquota.TierConfig{
        "pro": {
            MonthlyQuotas: map[string]int{
                "api_calls": 1000, // Monthly subscription quota
            },
            InitialForeverCredits: map[string]int{
                "api_calls": 25, // Sign-up bonus
            },
            ConsumptionOrder: []goquota.PeriodType{
                goquota.PeriodTypeMonthly, // Try subscription first
                goquota.PeriodTypeForever, // Fallback to credits
            },
        },
    },
}

manager, _ := goquota.NewManager(storage, &config)

// User has subscription + purchased credits
// Consumption automatically uses subscription first, then credits
newUsed, err := manager.Consume(ctx, "user123", "api_calls", 50, goquota.PeriodTypeAuto)
```

#### Storage Considerations

**PostgreSQL**: Forever periods use `NULL` for `period_end`. Run the migration:

```bash
psql -d goquota -f storage/postgres/migrations/002_forever_periods.sql
```

**Redis**: Forever periods have no TTL (they persist indefinitely until consumed).

**Firestore**: Forever periods have optional `periodEnd` field (omitted for forever credits).

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

### Admin Operations

`goquota` provides administrative methods for incident response and customer support operations.

**Set Usage** - Manually set quota usage (for corrections or resets):

```go
// Reset user's monthly usage to zero
err := manager.SetUsage(ctx, "user123", "api_calls", goquota.PeriodTypeMonthly, 0)

// Set to specific value (e.g., manual correction)
err = manager.SetUsage(ctx, "user123", "api_calls", goquota.PeriodTypeMonthly, 500)
```

**Grant One-Time Credits** - Give temporary credits without changing the plan:

```go
// Compensate for service outage
err := manager.GrantOneTimeCredit(ctx, "user123", "api_calls", 1000)

// These are added as forever credits (non-expiring)
```

**Reset Usage** - Quick reset to zero:

```go
// Reset monthly usage
err := manager.ResetUsage(ctx, "user123", "api_calls", goquota.PeriodTypeMonthly)

// Reset forever credits
err = manager.ResetUsage(ctx, "user123", "api_calls", goquota.PeriodTypeForever)
```

All admin operations are automatically logged if an audit logger is configured.

### Dry-Run / Shadow Mode

Test new quota rules without blocking real traffic. Perfect for validating configuration changes before enforcement.

```go
// Test mode: log violations but don't block
result, err := manager.Consume(
    ctx,
    "user123",
    "api_calls",
    1,
    goquota.PeriodTypeMonthly,
    goquota.WithDryRun(true), // Won't actually block on exceed
)

// Check if it would have been blocked
if err == goquota.ErrQuotaExceeded {
    log.Printf("Would have blocked user123 (dry-run mode)")
    // User's request continues normally
}
```

**Use Cases:**

- Gradual rollout: Enable for 10% of users, monitor, then expand
- A/B testing: Compare quota enforcement strategies
- Configuration validation: Test new limits before applying

**Per-Request Flexibility:**

```go
// Production enforcement for free tier
manager.Consume(ctx, userID, resource, amount, goquota.PeriodTypeMonthly)

// Shadow mode for enterprise (migration period)
if tier == "enterprise" {
    manager.Consume(ctx, userID, resource, amount, goquota.PeriodTypeMonthly,
        goquota.WithDryRun(true))
}
```

### Enhanced Consume Response

Get detailed usage information without extra storage calls, reducing Redis load by 50% for notification-heavy workloads.

**ConsumeWithResult** - Returns detailed usage breakdown:

```go
result, err := manager.ConsumeWithResult(
    ctx,
    "user123",
    "api_calls",
    1,
    goquota.PeriodTypeMonthly,
)

if err != nil {
    // Handle error
}

// Access detailed info without additional GetUsage() call
fmt.Printf("Used: %d/%d (%.1f%% - %d remaining)\n",
    result.NewUsed,
    result.Limit,
    result.Percentage,
    result.Remaining,
)

// Trigger notifications based on percentage
if result.Percentage >= 80.0 {
    sendWarningEmail(userID) // No extra storage call needed!
}
```

**GetUsageAfterConsume** - Convenience wrapper:

```go
usage, err := manager.GetUsageAfterConsume(
    ctx,
    "user123",
    "api_calls",
    1,
    goquota.PeriodTypeMonthly,
)

// Returns full Usage struct with limit, used, remaining, period info
```

### Audit Trail

Track all quota changes for compliance, debugging, and customer support.

**Automatic Logging** - Configure an audit logger:

```go
// Implement the AuditLogger interface or use a provided implementation
type CustomAuditLogger struct {
    db *sql.DB
}

func (l *CustomAuditLogger) LogAuditEntry(ctx context.Context, entry *goquota.AuditLogEntry) error {
    _, err := l.db.ExecContext(ctx,
        "INSERT INTO audit_logs (user_id, resource, action, amount, timestamp, actor, reason) VALUES ($1, $2, $3, $4, $5, $6, $7)",
        entry.UserID, entry.Resource, entry.Action, entry.Amount, entry.Timestamp, entry.Actor, entry.Reason,
    )
    return err
}

func (l *CustomAuditLogger) GetAuditLogs(ctx context.Context, filter goquota.AuditLogFilter) ([]*goquota.AuditLogEntry, error) {
    // Query and return audit logs based on filter
    // ...
}

// Set audit logger on manager
manager.SetAuditLogger(auditLogger)
```

**Query Audit History:**

```go
// Get all quota changes for a user
logs, err := manager.GetAuditLogs(ctx, goquota.AuditLogFilter{
    UserID:    "user123",
    StartTime: time.Now().Add(-30 * 24 * time.Hour), // Last 30 days
})

// Filter by resource
logs, err = manager.GetAuditLogs(ctx, goquota.AuditLogFilter{
    UserID:   "user123",
    Resource: "api_calls",
})

// View audit entry details
for _, log := range logs {
    fmt.Printf("%s: %s %s %d units (actor: %s, reason: %s)\n",
        log.Timestamp.Format(time.RFC3339),
        log.Action,      // "consume", "refund", "set_usage", "grant_credit"
        log.Resource,
        log.Amount,
        log.Actor,       // "system", "admin:john@company.com"
        log.Reason,      // "Normal consumption", "Service outage compensation"
    )
}
```

**Logged Actions:**

- Quota consumption (with idempotency key)
- Refunds (with reason)
- Admin operations (SetUsage, GrantOneTimeCredit, ResetUsage)
- Tier changes (with proration details)

### Clock Skew Protection

Prevent quota double-spending at reset boundaries in distributed systems using storage server time instead of application server time.

**TimeSource Interface** - Automatically used when available:

```go
// All storage backends implement TimeSource
type TimeSource interface {
    Now(ctx context.Context) (time.Time, error)
}

// Redis: Uses REDIS TIME command
// Firestore: Uses Firestore server timestamps
// Postgres: Uses PostgreSQL NOW()
// Memory: Falls back to time.Now() (for testing)
```

**Why It Matters:**

- Prevents "time travel" attacks from clock drift
- Ensures consistent period calculations across instances
- Critical for accurate billing at month/day boundaries

**No Configuration Required** - Works automatically:

```go
// Manager automatically uses storage server time if available
manager, err := goquota.NewManager(storage, &config)

// Period calculations use storage.Now(ctx) instead of time.Now()
// Quota resets happen at the exact same moment across all instances
```

**Fallback Behavior:**

- If storage doesn't implement TimeSource: falls back to `time.Now().UTC()`
- If storage.Now() fails: falls back to `time.Now().UTC()` with error logging

### Config Validation

Fail fast on startup with comprehensive configuration validation.

**Automatic Validation** - Runs on `NewManager()`:

```go
config := goquota.Config{
    DefaultTier: "free",
    Tiers: map[string]goquota.TierConfig{
        "free": {
            MonthlyQuotas: map[string]int{
                "api_calls": 100,
            },
        },
    },
}

// Validates:
// - DefaultTier exists in Tiers map
// - Quota values are >= 0
// - Tier names are valid
// - Rate limit configs are valid
// - Period types are valid
manager, err := goquota.NewManager(storage, &config)
if err != nil {
    // Detailed validation error
    // Example: "invalid config: default tier 'premium' not found in tiers map"
    log.Fatal(err)
}
```

**What's Validated:**

- ✅ Default tier exists
- ✅ No negative quota values
- ✅ Valid rate limit algorithms
- ✅ Non-zero rate limit rates
- ✅ Valid period types in consumption order
- ✅ Tier integrity (all referenced tiers exist)

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

## Billing Provider Integration

`goquota` includes a unified billing provider interface that automatically processes webhooks from payment providers (RevenueCat, Stripe, etc.) and updates user entitlements in real-time.

### Features

- **Provider Agnostic**: Switch between RevenueCat, Stripe, or any provider with zero code changes
- **Automatic Webhook Processing**: Real-time entitlement updates from payment providers
- **Idempotent**: Handles duplicate and out-of-order webhook deliveries safely
- **Secure**: Built-in rate limiting, DoS protection, and signature verification

### Quick Example

```go
import (
    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/revenuecat"
)

// Create billing provider
provider, _ := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "premium_monthly": "premium",
        "*":               "free",
    },
    Secret: os.Getenv("REVENUECAT_SECRET"),
})

// Register webhook endpoint
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())

// Sync user (Restore Purchases)
tier, _ := provider.SyncUser(ctx, userID)
```

See [pkg/billing/README.md](pkg/billing/README.md) for complete documentation.

## Usage API

The `pkg/api` package provides a standardized HTTP API for exposing user quota state to frontend applications. This transforms goquota from a backend library into a full-stack SaaS kit by providing ready-to-use endpoints that frontend developers can consume without understanding the underlying billing complexity.

### Features

- **Unified Quota View**: Combines monthly limits and forever credits into a single JSON response
- **Orphaned Credits Detection**: Automatically discovers and displays purchased credits even when users downgrade tiers
- **Unlimited Quota Handling**: Properly handles unlimited (-1) quotas
- **Resource Filtering**: Optional resource filtering for performance optimization

### Quick Example

```go
import (
    "net/http"
    "github.com/mihaimyh/goquota/pkg/api"
)

// Create API handler
usageHandler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
    KnownResources: []string{"api_calls", "gpt4", "tts_chars"},
})

// Register route
http.HandleFunc("/api/v1/me/usage", usageHandler.GetUsage)
```

### Response Format

The API returns a standardized JSON response:

```json
{
  "user_id": "user_123",
  "tier": "pro",
  "status": "active",
  "resources": {
    "api_calls": {
      "limit": 1500,
      "used": 150,
      "remaining": 1350,
      "reset_at": "2025-02-01T00:00:00Z",
      "breakdown": [
        {
          "source": "monthly",
          "limit": 1000,
          "used": 150
        },
        {
          "source": "forever",
          "balance": 500,
          "limit": 500,
          "used": 0
        }
      ]
    }
  }
}
```

The breakdown shows quota sources (monthly, forever) with their individual limits, usage, and balances, making it easy for frontend applications to display progress bars and usage details.

### Key Benefits

- **Frontend-Ready**: No need to understand hybrid billing complexity - just consume the JSON
- **Orphaned Credits**: Users never lose visibility of purchased credits after tier downgrades
- **Unlimited Support**: Correctly handles unlimited quotas (-1) in combined calculations
- **Performance**: Sequential fetching optimized for typical 1-3 resource scenarios

See [pkg/api/README.md](pkg/api/README.md) for complete documentation, including resource filtering, orphaned credits handling, and integration examples.

## Supported Frameworks

`goquota` provides native middleware for popular Go web frameworks:

- **Gin** - High-performance HTTP web framework
- **Echo** - High performance, minimalist Go web framework
- **Fiber** - Express-inspired web framework built on Fasthttp
- **Standard `net/http`** - Built-in Go HTTP server

All middleware implementations provide:

- Automatic quota and rate limit enforcement
- Standard rate limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`)
- Customizable error responses via callbacks
- Dynamic cost calculation based on request properties
- Framework-specific extractors for user ID, resource, and amount

## Billing Provider Webhooks

### Webhook Callbacks

Execute custom side effects after successful webhook processing, such as syncing Firebase Auth claims or sending notifications.

```go
import (
    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/stripe"
)

// Configure provider with webhook callback
stripeProvider, _ := stripe.NewProvider(stripe.Config{
    Config: billing.Config{
        Manager: manager,
        TierMapping: map[string]string{"price_abc123": "pro"},
        // Webhook callback for custom side effects
        WebhookCallback: func(ctx context.Context, event billing.WebhookEvent) error {
            // Example: Sync tier to Firebase Auth custom claims
            return authClient.SetCustomUserClaims(ctx, event.UserID, map[string]interface{}{
                "tier": event.NewTier,
                "subscription_expires_at": event.ExpiresAt,
            })
        },
    },
    StripeAPIKey:        os.Getenv("STRIPE_API_KEY"),
    StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
})
```

**Callback Execution Flow:**

1. Webhook signature is verified
2. Entitlement is updated in the database (**committed**)
3. **Callback is invoked** (if configured)
4. HTTP 200 response is sent to the webhook provider

> **⚠️ Best-Effort Semantics**: Due to timestamp-based idempotency, if a callback fails _after_ the entitlement is committed, webhook retries will be skipped. Implement your own idempotency or use async reconciliation for critical operations.

**WebhookEvent Structure:**

```go
type WebhookEvent struct {
    UserID         string                 // User identifier
    PreviousTier   string                 // Tier before the change
    NewTier        string                 // Tier after the change
    Provider       string                 // "stripe" or "revenuecat"
    EventType      string                 // Provider-specific event type
    EventTimestamp time.Time              // When the event occurred
    ExpiresAt      *time.Time             // Subscription expiration
    Metadata       map[string]interface{} // Provider-specific metadata
}
```

See [pkg/billing/README.md](pkg/billing/README.md) for complete documentation including Firebase Auth integration examples and best practices.

## HTTP Middleware

### Standard `net/http` Middleware

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

### Echo Framework Middleware

Native middleware for the [Echo](https://github.com/labstack/echo) web framework.

```go
import (
    "github.com/labstack/echo/v4"
    echoMiddleware "github.com/mihaimyh/goquota/middleware/echo"
)

// Create Echo instance
e := echo.New()

// Mock authentication middleware (sets UserID in context)
e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
    return func(c echo.Context) error {
        userID := c.Request().Header.Get("X-User-ID")
        if userID != "" {
            c.Set("UserID", userID) // Set in context for quota middleware
        }
        return next(c)
    }
})

// Apply quota middleware
api := e.Group("/api")
api.Use(echoMiddleware.Middleware(echoMiddleware.Config{
    Manager:     manager,
    GetUserID:   echoMiddleware.FromContext("UserID"), // Recommended: Extract from context
    GetResource: echoMiddleware.FixedResource("api_calls"),
    GetAmount:   echoMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeMonthly,
}))

api.GET("/data", func(c echo.Context) error {
    return c.String(200, "Data retrieved successfully")
})
```

**Echo-Specific Extractors:**

- `FromContext(key)` - Extract from Echo context (recommended for auth middleware integration)
- `FromHeader(headerName)` - Extract from HTTP header
- `FromParam(paramName)` - Extract from route parameter
- `FromQuery(queryName)` - Extract from query parameter

**Custom Error Responses:**

```go
api.Use(echoMiddleware.Middleware(echoMiddleware.Config{
    Manager:     manager,
    GetUserID:   echoMiddleware.FromContext("UserID"),
    GetResource: echoMiddleware.FixedResource("api_calls"),
    GetAmount:   echoMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeMonthly,
    OnQuotaExceeded: func(c echo.Context, usage *goquota.Usage) error {
        return c.JSON(http.StatusPaymentRequired, map[string]interface{}{
            "error": map[string]interface{}{
                "code":    "QUOTA_EXCEEDED",
                "message": "Monthly quota exceeded",
                "details": map[string]interface{}{
                    "used":  usage.Used,
                    "limit": usage.Limit,
                },
            },
        })
    },
    OnRateLimitExceeded: func(c echo.Context, retryAfter time.Duration, _ *goquota.RateLimitInfo) error {
        return c.JSON(http.StatusTooManyRequests, map[string]interface{}{
            "error":      "Rate limit exceeded",
            "retry_after": retryAfter.Seconds(),
        })
    },
}))
```

### Fiber Framework Middleware

Native middleware for the [Fiber](https://github.com/gofiber/fiber) web framework.

```go
import (
    "github.com/gofiber/fiber/v2"
    fiberMiddleware "github.com/mihaimyh/goquota/middleware/fiber"
)

// Create Fiber app
app := fiber.New()

// Mock authentication middleware (sets UserID in locals)
app.Use(func(c *fiber.Ctx) error {
    userID := c.Get("X-User-ID")
    if userID != "" {
        c.Locals("UserID", userID) // Set in locals for quota middleware
    }
    return c.Next()
})

// Apply quota middleware
api := app.Group("/api")
api.Use(fiberMiddleware.Middleware(fiberMiddleware.Config{
    Manager:     manager,
    GetUserID:   fiberMiddleware.FromLocals("UserID"), // Recommended: Extract from locals
    GetResource: fiberMiddleware.FixedResource("api_calls"),
    GetAmount:   fiberMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeMonthly,
}))

api.Get("/data", func(c *fiber.Ctx) error {
    return c.SendString("Data retrieved successfully")
})
```

**Fiber-Specific Extractors:**

- `FromLocals(key)` - Extract from Fiber locals (recommended for auth middleware integration)
- `FromHeader(headerName)` - Extract from HTTP header
- `FromParams(paramName)` - Extract from route parameter
- `FromQuery(queryName)` - Extract from query parameter

**Custom Error Responses:**

```go
api.Use(fiberMiddleware.Middleware(fiberMiddleware.Config{
    Manager:     manager,
    GetUserID:   fiberMiddleware.FromLocals("UserID"),
    GetResource: fiberMiddleware.FixedResource("api_calls"),
    GetAmount:   fiberMiddleware.FixedAmount(1),
    PeriodType:  goquota.PeriodTypeMonthly,
    OnQuotaExceeded: func(c *fiber.Ctx, usage *goquota.Usage) error {
        return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
            "error": fiber.Map{
                "code":    "QUOTA_EXCEEDED",
                "message": "Monthly quota exceeded",
                "details": fiber.Map{
                    "used":  usage.Used,
                    "limit": usage.Limit,
                },
            },
        })
    },
    OnRateLimitExceeded: func(c *fiber.Ctx, retryAfter time.Duration, _ *goquota.RateLimitInfo) error {
        return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
            "error":      "Rate limit exceeded",
            "retry_after": retryAfter.Seconds(),
        })
    },
}))
```

**Important Note for Fiber:**
Fiber uses `fasthttp` instead of `net/http`, so the middleware correctly bridges contexts using `c.UserContext()` to ensure compatibility with storage adapters that require standard `context.Context` (like Postgres transactions).

## Anniversary-Based Billing & Proration

`goquota` is designed for real-world subscription billing:

- **Anniversaries**: Cycles follow the user's subscription date (e.g. Jan 15 -> Feb 15), not calendar months.
- **Proration**: If a user upgrades headers mid-cycle, their usage carries over proportionally.

  > _Example_: User consumes 50% of Basic tier. Upgrading to Pro (10x limit) means they start with 50% of Pro usage, preserving the "percent used" fairness.

## Examples

See the [examples](examples/) directory:

- [Basic Usage](examples/basic/)
- [Redis Integration](examples/redis/)
- [PostgreSQL Integration](examples/postgres/)
- [Firestore Integration](examples/firestore/)
- [HTTP Server](examples/http-server/)
- [Gin Framework](examples/gin/)
- [Echo Framework](examples/echo/)
- [Fallback Strategies](examples/fallback/)
- [Rate Limiting](examples/rate-limiting/)
- [Comprehensive Example](examples/comprehensive/) - **All features in one example with Docker support**

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
