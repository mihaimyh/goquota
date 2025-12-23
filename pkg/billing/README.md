# Billing Provider Integration

The `pkg/billing` package provides a unified interface for integrating billing providers (RevenueCat, Stripe, PayPal, etc.) with `goquota`. This abstraction allows you to swap billing providers without changing your application logic.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [RevenueCat Implementation](#revenuecat-implementation)
- [Configuration](#configuration)
- [Webhook Setup](#webhook-setup)
- [User Synchronization](#user-synchronization)
- [Error Handling](#error-handling)
- [Best Practices](#best-practices)
- [Future Providers](#future-providers)

## Overview

The billing provider system automatically:
- ✅ Processes webhook events from payment providers
- ✅ Updates user entitlements in real-time
- ✅ Handles idempotency (duplicate/out-of-order events)
- ✅ Applies prorated quota adjustments for mid-cycle tier changes
- ✅ Supports "Restore Purchases" functionality
- ✅ Provides rate limiting and DoS protection

## Architecture

### Provider Interface

All billing providers implement the `billing.Provider` interface:

```go
type Provider interface {
    // Name returns the provider name (e.g., "revenuecat", "stripe")
    Name() string

    // WebhookHandler returns the HTTP handler that processes real-time events
    WebhookHandler() http.Handler

    // SyncUser forces a synchronization of the user's state from the provider
    // Returns the detected tier and any error
    SyncUser(ctx context.Context, userID string) (string, error)
}
```

### Benefits

1. **Provider Agnostic**: Switch between RevenueCat, Stripe, or any provider with zero code changes
2. **Automatic Updates**: Webhooks automatically update `goquota.Manager` with latest entitlements
3. **Idempotent**: Handles duplicate and out-of-order webhook deliveries safely
4. **Secure**: Built-in rate limiting, DoS protection, and signature verification

## Quick Start

### 1. Initialize the Manager

```go
import (
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/revenuecat"
    "github.com/mihaimyh/goquota/storage/memory"
)

// Create goquota manager
storage := memory.New()
config := &goquota.Config{
    DefaultTier: "explorer",
    Tiers: map[string]goquota.TierConfig{
        "explorer": {
            Name: "explorer",
            MonthlyQuotas: map[string]int{
                "api_calls": 100,
            },
        },
        "scholar": {
            Name: "scholar",
            MonthlyQuotas: map[string]int{
                "api_calls": 1000,
            },
        },
        "fluent": {
            Name: "fluent",
            MonthlyQuotas: map[string]int{
                "api_calls": 10000,
            },
        },
    },
}
manager, err := goquota.NewManager(storage, config)
if err != nil {
    log.Fatal(err)
}
```

### 2. Create Billing Provider

```go
// Create RevenueCat provider
provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "scholar_monthly": "scholar",
        "fluent_monthly":  "fluent",
        "*":               "explorer", // Default tier for unknown entitlements
    },
    WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
    APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
})
if err != nil {
    log.Fatal(err)
}
```

### 3. Register Webhook Handler

```go
import "net/http"

// Register webhook endpoint
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())

// Start server
log.Fatal(http.ListenAndServe(":8080", nil))
```

### 4. Sync User (Restore Purchases)

```go
func handleRestorePurchases(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r) // Your authentication logic
    
    tier, err := provider.SyncUser(r.Context(), userID)
    if err != nil {
        http.Error(w, "Failed to sync purchases", http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(map[string]string{
        "tier": tier,
    })
}
```

## RevenueCat Implementation

The RevenueCat provider is a complete implementation of the `billing.Provider` interface. See [pkg/billing/revenuecat/README.md](revenuecat/README.md) for detailed documentation.

### Quick Overview

- ✅ **Webhook Processing**: Handles all RevenueCat webhook event types
- ✅ **Timestamp-based Idempotency**: Prevents duplicate processing using event timestamps
- ✅ **HMAC Signature Verification**: Optional HMAC-SHA256 signature verification
- ✅ **Rate Limiting**: Built-in rate limiter (100 requests/minute per IP)
- ✅ **DoS Protection**: 256KB payload size limit
- ✅ **Memory Leak Prevention**: Automatic cleanup of rate limiter entries

### Quick Start

```go
import "github.com/mihaimyh/goquota/pkg/billing/revenuecat"

provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "premium_monthly": "premium",
        "*":               "free",
    },
    WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
    APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
})
```

For complete documentation, see [RevenueCat Provider Documentation](revenuecat/README.md).

## Configuration

### Config Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Manager` | `*goquota.Manager` | Yes | The goquota Manager instance |
| `TierMapping` | `map[string]string` | Yes | Maps provider IDs to goquota tiers |
| `WebhookSecret` | `string` | Yes | Webhook secret for verifying incoming webhook requests |
| `APIKey` | `string` | Yes | API key for outbound API calls (e.g. SyncUser) |
| `HTTPClient` | `*http.Client` | No | Custom HTTP client (default: 10s timeout) |
| `EnableHMAC` | `bool` | No | Enable HMAC signature verification |

### Tier Mapping Examples

```go
// Simple mapping
TierMapping: map[string]string{
    "premium_monthly": "premium",
    "premium_annual":  "premium",
}

// With default tier
TierMapping: map[string]string{
    "premium_monthly": "premium",
    "*":               "free", // Unknown entitlements → free tier
}

// Multiple tiers
TierMapping: map[string]string{
    "basic_monthly":   "basic",
    "pro_monthly":      "pro",
    "enterprise_monthly": "enterprise",
    "default":         "free", // Alternative to "*"
}
```

## Webhook Setup

### 1. Configure RevenueCat Webhook

In your RevenueCat dashboard:
1. Go to **Project Settings** → **Webhooks**
2. Add webhook URL: `https://your-domain.com/webhooks/revenuecat`
3. Select events to receive (recommended: all events)
4. Copy the webhook secret

### 2. Environment Variables

```bash
export REVENUECAT_WEBHOOK_SECRET="yWt"
export REVENUECAT_SECRET_API_KEY="sk_"
export REVENUECAT_ENABLE_HMAC="false"  # Optional
```

### 3. Register Handler

```go
// Using standard library
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())

// Using Gorilla Mux
router.Handle("/webhooks/revenuecat", provider.WebhookHandler()).Methods("POST")

// Using Gin
router.POST("/webhooks/revenuecat", gin.WrapH(provider.WebhookHandler()))

// Using Echo
e.POST("/webhooks/revenuecat", echo.WrapHandler(provider.WebhookHandler()))
```

### 4. Security Headers

The webhook handler automatically sets security headers:
- `Cache-Control: no-store`
- `X-Content-Type-Options: nosniff`

### 5. Rate Limiting

Built-in rate limiting: **100 requests per minute per IP address**

This prevents:
- DDoS attacks
- Webhook replay attacks
- Accidental webhook spam

### 6. Payload Size Limit

Maximum webhook payload size: **256KB**

Larger payloads are rejected with `413 Request Entity Too Large`.

## User Synchronization

### SyncUser Method

The `SyncUser` method fetches the latest entitlement state from RevenueCat and updates the `goquota.Manager`:

```go
tier, err := provider.SyncUser(ctx, userID)
if err != nil {
    // Handle error
}
// tier contains the detected tier (e.g., "scholar", "fluent", "explorer")
```

### Use Cases

1. **Restore Purchases**: When user taps "Restore Purchases"
   ```go
   func handleRestorePurchases(w http.ResponseWriter, r *http.Request) {
       userID := getUserID(r)
       tier, err := provider.SyncUser(r.Context(), userID)
       if err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }
       json.NewEncoder(w).Encode(map[string]string{"tier": tier})
   }
   ```

2. **Nightly Reconciliation**: Batch job to sync all users
   ```go
   func reconcileUsers(ctx context.Context, userIDs []string) {
       for _, userID := range userIDs {
           tier, err := provider.SyncUser(ctx, userID)
           if err != nil {
               log.Printf("Failed to sync user %s: %v", userID, err)
               continue
           }
           log.Printf("User %s synced to tier %s", userID, tier)
       }
   }
   ```

3. **Admin Dashboard**: Manual sync for troubleshooting
   ```go
   func adminSyncUser(w http.ResponseWriter, r *http.Request) {
       userID := r.URL.Query().Get("user_id")
       tier, err := provider.SyncUser(r.Context(), userID)
       if err != nil {
           http.Error(w, err.Error(), http.StatusInternalServerError)
           return
       }
       fmt.Fprintf(w, "User %s synced to tier %s", userID, tier)
   }
   ```

### Behavior

- **User Found**: Updates entitlement with latest tier from RevenueCat
- **User Not Found**: Sets user to default tier (`explorer`)
- **API Error**: Returns error, entitlement unchanged
- **Network Error**: Returns error with timeout

## Error Handling

### Error Types

```go
import "github.com/mihaimyh/goquota/pkg/billing"

// Provider configuration errors
if err == billing.ErrProviderNotConfigured {
    // Manager is nil or invalid config
}

// Webhook errors
if err == billing.ErrInvalidWebhookSignature {
    // Signature verification failed
}

if err == billing.ErrInvalidWebhookPayload {
    // Payload parsing failed
}

// API errors
if err == billing.ErrUserNotFound {
    // User not found in RevenueCat
}

if err == billing.ErrProviderAPIError {
    // RevenueCat API returned an error
}
```

### Error Handling Example

```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
    // The webhook handler automatically handles errors
    // Returns appropriate HTTP status codes:
    // - 200: Success
    // - 400: Invalid payload
    // - 401: Unauthorized (invalid signature)
    // - 413: Payload too large
    // - 429: Rate limited
    // - 500: Internal server error
    provider.WebhookHandler().ServeHTTP(w, r)
}
```

## Idempotency

The billing provider uses **timestamp-based idempotency** to handle duplicate and out-of-order webhook deliveries.

### How It Works

1. Each webhook event includes a `timestamp_ms` field
2. The provider stores this timestamp as `Entitlement.UpdatedAt`
3. Before updating, it checks: `if eventTimestamp <= existing.UpdatedAt { skip }`
4. Only newer events are processed

### Benefits

- ✅ **Duplicate Events**: Same event sent twice → processed once
- ✅ **Out-of-Order Delivery**: Older events are ignored
- ✅ **No External Storage**: No need for event ID database
- ✅ **Automatic**: Works transparently, no configuration needed

### Example

```go
// Event 1: timestamp_ms = 1000, tier = "scholar"
// → Processed, UpdatedAt = 1000

// Event 2: timestamp_ms = 1000 (duplicate)
// → Skipped (timestamp not newer)

// Event 3: timestamp_ms = 500 (out-of-order)
// → Skipped (timestamp older than existing)

// Event 4: timestamp_ms = 2000, tier = "fluent"
// → Processed, UpdatedAt = 2000
```

## Best Practices

### 1. Tier Mapping

- **Use explicit mappings** for known entitlements
- **Always include a default** (`"*"` or `"default"`) for unknown entitlements
- **Keep mappings in sync** with your RevenueCat products

```go
TierMapping: map[string]string{
    "premium_monthly": "premium",
    "premium_annual":  "premium",
    "*":               "free", // Safety net
}
```

### 2. Secret Management

- **Never commit secrets** to version control
- **Use environment variables** or secret management systems
- **Rotate secrets** periodically
- **Use separate secrets** for webhooks and API calls

```go
WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
```

### 3. Error Handling

- **Log webhook errors** for debugging
- **Monitor webhook success rates**
- **Set up alerts** for repeated failures

```go
func logWebhookError(err error, userID string) {
    log.Printf("[WEBHOOK] user=%s error=%v", userID, err)
    // Send to monitoring system (e.g., Sentry, Datadog)
}
```

### 4. Testing

- **Use test events**: RevenueCat sends `TEST` events that are automatically acknowledged
- **Mock HTTP client**: For testing `SyncUser` without calling real API
- **Test idempotency**: Send same webhook twice, verify state unchanged

```go
// Test webhook idempotency
func TestWebhookIdempotency(t *testing.T) {
    // Send webhook twice
    // Verify entitlement unchanged
}
```

### 5. Monitoring

- **Track webhook volume**: Monitor request rates
- **Monitor sync operations**: Track `SyncUser` success/failure rates
- **Alert on errors**: Set up alerts for repeated failures

### 6. Grace Periods

RevenueCat may send events during grace periods (e.g., `BILLING_ISSUE`, `CANCELLATION`). The provider:
- **Keeps tier active** if expiration is in the future
- **Downgrades to default** if expiration is in the past

This ensures users retain access during grace periods but are downgraded after expiration.

## Future Providers

The `billing.Provider` interface is designed to support multiple providers. Future implementations:

### Stripe (Planned)

```go
import "github.com/mihaimyh/goquota/pkg/billing/stripe"

provider, err := stripe.NewProvider(billing.Config{
    Manager:       manager,
    TierMapping:   map[string]string{
        "price_premium": "premium",
    },
    WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
    APIKey:        os.Getenv("STRIPE_API_KEY"),
})
```

### PayPal (Planned)

```go
import "github.com/mihaimyh/goquota/pkg/billing/paypal"

provider, err := paypal.NewProvider(billing.Config{
    Manager:       manager,
    TierMapping:   map[string]string{
        "premium_subscription": "premium",
    },
    WebhookSecret: os.Getenv("PAYPAL_WEBHOOK_SECRET"),
    APIKey:        os.Getenv("PAYPAL_CLIENT_SECRET"),
})
```

### Switching Providers

Switching providers requires only configuration changes:

```go
// Before: RevenueCat
provider, _ := revenuecat.NewProvider(config)

// After: Stripe
provider, _ := stripe.NewProvider(config)

// Application code unchanged!
http.Handle("/webhooks/billing", provider.WebhookHandler())
tier, _ := provider.SyncUser(ctx, userID)
```

## Examples

### Complete Example

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    
    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/revenuecat"
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

func main() {
    // 1. Create goquota manager
    storage := memory.New()
    config := &goquota.Config{
        DefaultTier: "explorer",
        Tiers: map[string]goquota.TierConfig{
            "explorer": {
                Name: "explorer",
                MonthlyQuotas: map[string]int{"api_calls": 100},
            },
            "scholar": {
                Name: "scholar",
                MonthlyQuotas: map[string]int{"api_calls": 1000},
            },
        },
    }
    manager, _ := goquota.NewManager(storage, config)
    
    // 2. Create billing provider
    provider, _ := revenuecat.NewProvider(billing.Config{
        Manager: manager,
        TierMapping: map[string]string{
            "scholar_monthly": "scholar",
            "*":               "explorer",
        },
        WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
        APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
    })
    
    // 3. Register webhook
    http.Handle("/webhooks/revenuecat", provider.WebhookHandler())
    
    // 4. Register restore purchases endpoint
    http.HandleFunc("/restore-purchases", func(w http.ResponseWriter, r *http.Request) {
        userID := r.URL.Query().Get("user_id")
        tier, err := provider.SyncUser(r.Context(), userID)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{"tier": tier})
    })
    
    // 5. Start server
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### With Custom HTTP Client

```go
import (
    "net/http"
    "time"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Custom HTTP client with OpenTelemetry instrumentation
httpClient := &http.Client{
    Timeout: 15 * time.Second,
    Transport: otelhttp.NewTransport(http.DefaultTransport),
}

provider, _ := revenuecat.NewProvider(billing.Config{
    Manager:       manager,
    TierMapping:   tierMapping,
    WebhookSecret: webhookSecret,
    APIKey:        apiKey,
    HTTPClient:    httpClient,
})
```

### With HMAC Verification

```go
provider, _ := revenuecat.NewProvider(billing.Config{
    Manager:       manager,
    TierMapping:   tierMapping,
    WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
    APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
    EnableHMAC:    true, // Enable HMAC signature verification
})
```

## Troubleshooting

### Webhook Not Processing

1. **Check authentication**: Verify `WebhookSecret` matches RevenueCat webhook secret
2. **Check endpoint**: Ensure webhook URL is accessible
3. **Check logs**: Look for error messages in webhook handler
4. **Test event**: Send a `TEST` event from RevenueCat dashboard

### SyncUser Returns Default Tier

1. **User not found**: User may not exist in RevenueCat
2. **No active entitlements**: User's subscription may have expired
3. **API error**: Check RevenueCat API status and credentials

### Tier Not Updating

1. **Check tier mapping**: Verify entitlement ID is in `TierMapping`
2. **Check webhook events**: Verify events are being received
3. **Check idempotency**: Older events may be ignored (check timestamps)
4. **Check manager**: Verify `Manager.SetEntitlement` is working

## API Reference

### Provider Interface

```go
type Provider interface {
    Name() string
    WebhookHandler() http.Handler
    SyncUser(ctx context.Context, userID string) (string, error)
}
```

### Config

```go
type Config struct {
    Manager       *goquota.Manager
    TierMapping   map[string]string
    WebhookSecret string
    APIKey        string
    HTTPClient    *http.Client  // Optional
    EnableHMAC    bool          // Optional
}
```

### Errors

```go
var (
    ErrProviderNotConfigured  = errors.New("billing provider not configured")
    ErrInvalidWebhookSignature = errors.New("invalid webhook signature")
    ErrInvalidWebhookPayload   = errors.New("invalid webhook payload")
    ErrUserNotFound            = errors.New("user not found in billing provider")
    ErrProviderAPIError         = errors.New("billing provider API error")
)
```

## License

This package is part of the `goquota` project and follows the same license.

