# goquota

[![Go Reference](https://pkg.go.dev/badge/github.com/mihaimyh/goquota.svg)](https://pkg.go.dev/github.com/mihaimyh/goquota)
[![Go Report Card](https://goreportcard.com/badge/github.com/mihaimyh/goquota)](https://goreportcard.com/report/github.com/mihaimyh/goquota)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Subscription quota management for Go with anniversary-based billing cycles, prorated tier changes, and pluggable storage.

## Features

- **Anniversary-based billing cycles** - Preserve subscription anniversary dates across months
- **Prorated quota adjustments** - Handle mid-cycle tier changes fairly
- **Multiple quota types** - Support both daily and monthly quotas
- **Pluggable storage** - Firestore, in-memory, or custom backends
- **Transaction-safe** - Prevent over-consumption with atomic operations
- **HTTP middleware** - Easy integration with web frameworks
- **Production-ready** - 73%+ test coverage, used in production

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
    manager, _ := goquota.NewManager(storage, config)
    
    // Set user entitlement
    ctx := context.Background()
    manager.SetEntitlement(ctx, &goquota.Entitlement{
        UserID: "user123",
        Tier:   "pro",
        SubscriptionStartDate: time.Now().UTC(),
    })
    
    // Consume quota
    err := manager.Consume(ctx, "user123", "api_calls", 1, goquota.PeriodTypeMonthly)
    if err == goquota.ErrQuotaExceeded {
        // Handle quota exceeded
    }
}
```

## Storage Adapters

### In-Memory (Testing)

```go
import "github.com/mihaimyh/goquota/storage/memory"

storage := memory.New()
```

### Firestore (Production)

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

## HTTP Middleware

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
})

// Apply to handler
http.Handle("/api/endpoint", quotaMiddleware(yourHandler))
```

## Anniversary-Based Billing

Unlike simple monthly quotas that reset on the 1st of each month, goquota preserves subscription anniversary dates:

```
User subscribed on Jan 15
 Cycle 1: Jan 15 - Feb 15
 Cycle 2: Feb 15 - Mar 15
 Cycle 3: Mar 15 - Apr 15
 ...
```

This ensures fair billing aligned with subscription dates, even for month-end edge cases (e.g., Jan 31  Feb 28).

## Prorated Tier Changes

When users upgrade or downgrade mid-cycle, quotas are adjusted proportionally:

```
User on "scholar" tier (3600 seconds/month)
 Day 1-10: Consumed 1000 seconds
 Day 10: Upgrade to "fluent" (18000 seconds/month)
 New limit: 1000 (used) + (18000  20/30 days remaining) = 13000 seconds
```

## Examples

See the [examples](examples/) directory for complete working examples:

- [Basic Usage](examples/basic/) - Simple quota management
- [Firestore Integration](examples/firestore/) - Production setup with Firestore
- [HTTP Server](examples/http-server/) - Web API with quota enforcement

## API Reference

### Manager

```go
// Create manager
manager, err := goquota.NewManager(storage, config)

// Get current billing cycle
period, err := manager.GetCurrentCycle(ctx, userID)

// Get quota status
usage, err := manager.GetQuota(ctx, userID, "resource", goquota.PeriodTypeMonthly)

// Consume quota
err := manager.Consume(ctx, userID, "resource", amount, goquota.PeriodTypeMonthly)

// Apply tier change
err := manager.ApplyTierChange(ctx, userID, "old_tier", "new_tier", "resource")

// Manage entitlements
err := manager.SetEntitlement(ctx, entitlement)
ent, err := manager.GetEntitlement(ctx, userID)
```

### Storage Interface

Implement custom storage backends:

```go
type Storage interface {
    GetEntitlement(ctx context.Context, userID string) (*Entitlement, error)
    SetEntitlement(ctx context.Context, ent *Entitlement) error
    GetUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error)
    ConsumeQuota(ctx context.Context, req *ConsumeRequest) error
    ApplyTierChange(ctx context.Context, req *TierChangeRequest) error
}
```

## Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package tests
go test ./pkg/goquota/...
go test ./storage/memory/...
```

## Contributing

Contributions welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure `golangci-lint` passes
5. Submit a pull request

## License

MIT License - see [LICENSE](LICENSE) for details

## Acknowledgments

Extracted from a production SaaS application managing subscription quotas for thousands of users.