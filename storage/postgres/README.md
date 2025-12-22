# PostgreSQL Storage Adapter

The PostgreSQL storage adapter provides a SQL-based implementation of the `goquota.Storage` interface. This adapter is ideal for applications that already use PostgreSQL and want to avoid adding Redis or other external dependencies.

## Features

- **ACID Transactions**: Uses PostgreSQL transactions with `SELECT ... FOR UPDATE` for atomic quota operations
- **Composite Pattern**: Automatically delegates rate limiting to in-memory storage for performance
- **Scoped Idempotency**: Idempotency keys are scoped per user, allowing safe reuse across users
- **Automatic Cleanup**: Background cleanup of expired audit records
- **Connection Pooling**: Efficient connection management via `pgxpool`

## Architecture

### Quota Management (SQL-based)

Monthly and daily quotas are stored in PostgreSQL and synchronized globally across all instances. This ensures consistent quota tracking in distributed deployments.

### Rate Limiting (In-Memory)

Rate limiting (requests/second) is handled by an embedded in-memory storage adapter. This is necessary because SQL databases are too slow for high-frequency rate limit checks (1000+ RPS).

**Important**: Rate limits are **local per instance**, not global. In a cluster of N instances, the effective total rate limit is `N × ConfiguredRate`.

For example:
- If you configure 10 requests/second per instance
- And you have 3 instances
- The total effective rate limit is 30 requests/second across all instances

This is by design - rate limiting is for DDoS protection and should be fast, while quotas are for billing and require global consistency.

## Installation

```bash
go get github.com/mihaimyh/goquota/storage/postgres
```

## Database Setup

### 1. Create Database

```sql
CREATE DATABASE goquota;
```

### 2. Run Migrations

Execute the migration file to create the required tables:

```bash
psql -d goquota -f storage/postgres/migrations/001_initial_schema.sql
```

Or manually run the SQL from `storage/postgres/migrations/001_initial_schema.sql`.

### 3. Required Tables

The schema creates the following tables:
- `entitlements` - User subscription tiers
- `quota_usage` - Quota consumption tracking
- `consumption_records` - Audit trail for consumption (with expiration)
- `refund_records` - Audit trail for refunds (with expiration)

## Connection String

Ensure your connection string includes pool configuration if you don't set it in the config struct:

```
postgres://user:password@localhost:5432/goquota?pool_max_conns=10&sslmode=disable
```

Alternatively, configure the pool via the `Config` struct (recommended):

```go
config.MaxConns = 20
config.MinConns = 5
```

## Usage

### Basic Example

```go
package main

import (
    "context"
    "time"
    
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/postgres"
)

func main() {
    ctx := context.Background()
    
    // Create PostgreSQL storage
    config := postgres.DefaultConfig()
    config.ConnectionString = "postgres://user:password@localhost:5432/goquota?sslmode=disable"
    config.CleanupEnabled = true
    config.CleanupInterval = 1 * time.Hour
    config.RecordTTL = 7 * 24 * time.Hour // 7 days
    
    storage, err := postgres.New(ctx, config)
    if err != nil {
        log.Fatal(err)
    }
    defer storage.Close() // Important: closes connection pool and cleanup goroutine
    
    // Create manager
    managerConfig := &goquota.Config{
        Tiers: map[string]goquota.TierConfig{
            "free": {
                Name: "free",
                MonthlyQuotas: map[string]int{
                    "api_calls": 1000,
                },
            },
        },
        DefaultTier: "free",
    }
    
    manager, err := goquota.NewManager(storage, managerConfig)
    if err != nil {
        log.Fatal(err)
    }
    
    // Use manager as normal
    usage, err := manager.Consume(ctx, "user123", "api_calls", 10, goquota.PeriodTypeMonthly)
    // ...
}
```

### Configuration Options

```go
type Config struct {
    // ConnectionString is the PostgreSQL connection string
    ConnectionString string
    
    // Pool configuration
    MaxConns        int32         // Maximum connections (default: 10)
    MinConns        int32         // Minimum connections (default: 2)
    MaxConnLifetime time.Duration // Max connection lifetime (default: 1 hour)
    MaxConnIdleTime time.Duration // Max idle time (default: 30 minutes)
    
    // Cleanup configuration
    CleanupEnabled  bool          // Enable background cleanup (default: true)
    CleanupInterval time.Duration // How often to run cleanup (default: 1 hour)
    RecordTTL       time.Duration // TTL for audit records (default: 7 days)
}
```

## Important Notes

### Distributed vs Local Limits

- **Quotas (PostgreSQL)**: Global and synchronized. If Instance A consumes 10 units, Instance B sees 10 units consumed.
- **Rate Limits (In-Memory)**: Local per instance. If Instance A allows 10 req/sec, Instance B also allows 10 req/sec independently.

### Idempotency Key Scoping

Idempotency keys are scoped to `user_id`, not globally unique. This means:
- User A can use key "order-123"
- User B can also use key "order-123"
- Both will work correctly

If you need globally unique keys, use UUIDs or include user ID in the key.

### Cleanup

The adapter automatically cleans up expired consumption and refund records based on the `expires_at` column. You can also trigger cleanup manually:

```go
err := storage.Cleanup(ctx)
```

### Connection Management

Always call `storage.Close()` when shutting down to:
1. Stop the background cleanup goroutine
2. Close the PostgreSQL connection pool

## Performance Considerations

### Quota Operations

- `ConsumeQuota` uses transactions with row-level locking (`SELECT ... FOR UPDATE`)
- Suitable for billing/quota tracking (typically < 1000 ops/sec per user)
- Not suitable for high-frequency rate limiting (1000+ RPS)

### Rate Limiting

- Automatically uses in-memory storage (fast, but local to instance)
- No database queries for rate limit checks
- Suitable for DDoS protection

### Connection Pooling

The adapter uses `pgxpool` for efficient connection management. Configure pool size based on your expected load:

```go
config.MaxConns = 20  // Increase for higher load
config.MinConns = 5   // Keep warm connections
```

## Testing

Tests require a PostgreSQL instance. Set the `POSTGRES_TEST_DSN` environment variable:

```bash
export POSTGRES_TEST_DSN="postgres://user:password@localhost:5432/goquota_test?sslmode=disable"
go test ./storage/postgres/...
```

Or use the default connection string (localhost with default credentials).

## Migration from Other Storage Adapters

The PostgreSQL adapter implements the same `Storage` interface as Redis and Memory adapters. You can switch storage backends without changing your Manager code:

```go
// Before (Redis)
storage, _ := redis.New(client, redis.DefaultConfig())

// After (PostgreSQL)
storage, _ := postgres.New(ctx, postgres.DefaultConfig())

// Manager code remains the same
manager, _ := goquota.NewManager(storage, config)
```

## Troubleshooting

### Connection Errors

Ensure your connection string is correct and PostgreSQL is accessible:

```go
// Test connection
err := storage.Ping(ctx)
if err != nil {
    log.Fatal("Database connection failed:", err)
}
```

### Transaction Deadlocks

If you see deadlock errors, ensure you're not holding transactions open too long. The adapter uses short-lived transactions for quota operations.

### Cleanup Not Running

If cleanup isn't running, check:
1. `CleanupEnabled` is `true`
2. `CleanupInterval` is set appropriately
3. `RecordTTL` is set (records need expiration time)

## License

Same as the main goquota library.

