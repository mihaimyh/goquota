# RevenueCat Billing Provider

The RevenueCat provider (`pkg/billing/revenuecat`) implements the `billing.Provider` interface for RevenueCat, enabling automatic subscription management and quota enforcement with `goquota`.

## Table of Contents

- [Quick Start](#quick-start)
- [Installation](#installation)
- [Configuration](#configuration)
- [Webhook Setup](#webhook-setup)
- [User Synchronization](#user-synchronization)
- [Event Types](#event-types)
- [Security](#security)
- [Tier Mapping](#tier-mapping)
- [Examples](#examples)
- [Troubleshooting](#troubleshooting)
- [API Reference](#api-reference)

## Quick Start

### 1. Install Dependencies

```bash
go get github.com/mihaimyh/goquota/pkg/billing/revenuecat
```

### 2. Create Provider

```go
import (
    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/revenuecat"
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

// Create goquota manager
storage := memory.New()
manager, _ := goquota.NewManager(storage, &goquota.Config{
    DefaultTier: "explorer",
    Tiers: map[string]goquota.TierConfig{
        "explorer": { /* ... */ },
        "scholar":  { /* ... */ },
        "fluent":   { /* ... */ },
    },
})

// Create RevenueCat provider
provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "scholar_monthly": "scholar",
        "fluent_monthly":  "fluent",
        "*":               "explorer",
    },
    Secret: os.Getenv("REVENUECAT_SECRET"),
})
if err != nil {
    log.Fatal(err)
}
```

### 3. Register Webhook

```go
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())
```

### 4. Sync User (Optional)

```go
tier, err := provider.SyncUser(ctx, userID)
```

## Installation

```bash
go get github.com/mihaimyh/goquota/pkg/billing/revenuecat
```

## Configuration

### Required Fields

- **Manager**: The `goquota.Manager` instance that will be updated
- **TierMapping**: Maps RevenueCat entitlement/product IDs to goquota tiers
- **Secret**: RevenueCat API secret (Bearer token)

### Optional Fields

- **HTTPClient**: Custom HTTP client (default: 10s timeout)
- **EnableHMAC**: Enable HMAC signature verification (default: false)

### Basic Configuration

```go
provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "premium_monthly": "premium",
        "premium_annual":  "premium",
    },
    Secret: "rcsk_...", // RevenueCat API secret
})
```

### Advanced Configuration

```go
import (
    "net/http"
    "time"
)

// Custom HTTP client with longer timeout
httpClient := &http.Client{
    Timeout: 30 * time.Second,
}

provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        "premium_monthly": "premium",
        "*":               "free",
    },
    Secret: os.Getenv("REVENUECAT_SECRET"),
    HTTPClient: httpClient,
    EnableHMAC: false, // Use Bearer token (default)
})
```

### HMAC Signature Verification

For enhanced security, enable HMAC signature verification:

```go
provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: tierMapping,
    Secret: os.Getenv("REVENUECAT_HMAC_SECRET"),
    EnableHMAC: true, // Enable HMAC verification
})
```

When `EnableHMAC` is `true`, the provider expects HMAC-SHA256 signatures in the `X-RevenueCat-Signature` header.

## Webhook Setup

### Step 1: Configure RevenueCat Dashboard

1. Log in to [RevenueCat Dashboard](https://app.revenuecat.com)
2. Navigate to **Project Settings** → **Webhooks**
3. Click **Add Webhook**
4. Enter your webhook URL: `https://your-domain.com/webhooks/revenuecat`
5. Select events to receive (recommended: **All Events**)
6. Copy the **Webhook Secret** (starts with `rcsk_`)

### Step 2: Set Environment Variable

```bash
export REVENUECAT_SECRET="rcsk_..."
```

### Step 3: Register Handler

#### Standard `net/http`

```go
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())
http.ListenAndServe(":8080", nil)
```

#### Gorilla Mux

```go
import "github.com/gorilla/mux"

router := mux.NewRouter()
router.Handle("/webhooks/revenuecat", provider.WebhookHandler()).Methods("POST")
http.ListenAndServe(":8080", router)
```

#### Gin

```go
import "github.com/gin-gonic/gin"

router := gin.Default()
router.POST("/webhooks/revenuecat", gin.WrapH(provider.WebhookHandler()))
router.Run(":8080")
```

#### Echo

```go
import "github.com/labstack/echo/v4"

e := echo.New()
e.POST("/webhooks/revenuecat", echo.WrapHandler(provider.WebhookHandler()))
e.Start(":8080")
```

### Step 4: Test Webhook

RevenueCat sends a `TEST` event when you add a webhook. The provider automatically acknowledges test events without processing them.

### Webhook Security

The webhook handler includes:

- **Rate Limiting**: 100 requests per minute per IP address
- **DoS Protection**: Maximum payload size of 256KB
- **Signature Verification**: Bearer token or HMAC signature validation
- **Security Headers**: `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`

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

#### 1. Restore Purchases

When a user taps "Restore Purchases" in your app:

```go
func handleRestorePurchases(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r) // Your authentication logic
    
    tier, err := provider.SyncUser(r.Context(), userID)
    if err != nil {
        http.Error(w, "Failed to restore purchases", http.StatusInternalServerError)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{
        "tier": tier,
        "status": "restored",
    })
}
```

#### 2. Admin Dashboard

Manual sync for troubleshooting:

```go
func adminSyncUser(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("user_id")
    if userID == "" {
        http.Error(w, "user_id required", http.StatusBadRequest)
        return
    }
    
    tier, err := provider.SyncUser(r.Context(), userID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    fmt.Fprintf(w, "User %s synced to tier: %s", userID, tier)
}
```

#### 3. Nightly Reconciliation

Batch job to sync all users:

```go
func reconcileUsers(ctx context.Context, userIDs []string) error {
    for _, userID := range userIDs {
        tier, err := provider.SyncUser(ctx, userID)
        if err != nil {
            log.Printf("Failed to sync user %s: %v", userID, err)
            continue
        }
        log.Printf("User %s synced to tier %s", userID, tier)
    }
    return nil
}
```

### Behavior

- **User Found with Active Subscription**: Updates entitlement with latest tier
- **User Found with Expired Subscription**: Sets to default tier
- **User Not Found**: Sets to default tier (no error)
- **API Error**: Returns error, entitlement unchanged
- **Network Error**: Returns error with timeout

## Event Types

The RevenueCat provider handles the following webhook event types:

### INITIAL_PURCHASE

Triggered when a user makes their first purchase.

**Behavior**: Sets user to the mapped tier, establishes subscription start date.

### RENEWAL

Triggered when a subscription renews.

**Behavior**: Updates entitlement expiration date, keeps tier active.

### CANCELLATION

Triggered when a subscription is cancelled.

**Behavior**: 
- If expiration is in the future: Keeps tier active (grace period)
- If expiration is in the past: Downgrades to default tier

### EXPIRATION

Triggered when a subscription expires.

**Behavior**: Downgrades user to default tier.

### BILLING_ISSUE

Triggered when there's a payment issue.

**Behavior**: 
- If expiration is in the future: Keeps tier active (grace period)
- If expiration is in the past: Downgrades to default tier

### SUBSCRIPTION_PAUSED

Triggered when a subscription is paused.

**Behavior**: 
- If expiration is in the future: Keeps tier active
- If expiration is in the past: Downgrades to default tier

### TEST

Triggered when testing webhook configuration.

**Behavior**: Automatically acknowledged, not processed (returns `200 OK`).

## Security

### Authentication Methods

#### 1. Bearer Token (Default)

Simple token matching using the RevenueCat API secret:

```go
Secret: "rcsk_..." // RevenueCat API secret
EnableHMAC: false  // Default
```

The provider checks the `Authorization: Bearer <token>` header.

#### 2. HMAC Signature (Optional)

Cryptographic signature verification using HMAC-SHA256:

```go
Secret: "your_hmac_secret"
EnableHMAC: true
```

The provider verifies signatures in the `X-RevenueCat-Signature` header.

### Rate Limiting

Built-in rate limiter: **100 requests per minute per IP address**

This prevents:
- DDoS attacks
- Webhook replay attacks
- Accidental webhook spam

Rate-limited requests receive `429 Too Many Requests`.

### Payload Size Limit

Maximum webhook payload size: **256KB**

Larger payloads are rejected with `413 Request Entity Too Large`.

### Security Headers

The webhook handler automatically sets:
- `Cache-Control: no-store`
- `X-Content-Type-Options: nosniff`

## Tier Mapping

### Basic Mapping

Map RevenueCat entitlement/product IDs to your goquota tiers:

```go
TierMapping: map[string]string{
    "scholar_monthly": "scholar",
    "scholar_annual":  "scholar",
    "fluent_monthly":  "fluent",
    "fluent_annual":   "fluent",
}
```

### Default Tier

Use `"*"` or `"default"` to handle unknown entitlements:

```go
TierMapping: map[string]string{
    "premium_monthly": "premium",
    "*":               "free", // Unknown entitlements → free tier
}
```

### Case Insensitive

Mappings are case-insensitive:

```go
// All of these map to "scholar":
"scholar_monthly" → "scholar"
"SCHOLAR_MONTHLY" → "scholar"
"Scholar_Monthly" → "scholar"
```

### Partial Matching

If no exact match is found, the provider tries partial matching:

```go
// If entitlement ID is "premium_monthly_usd"
// And mapping has "premium_monthly"
// It will match via partial matching
```

### Best Practices

1. **Always include a default**: Use `"*"` or `"default"` for safety
2. **Map all known entitlements**: Explicit mappings are clearer
3. **Keep mappings in sync**: Update when adding new RevenueCat products
4. **Use consistent naming**: Match RevenueCat product IDs exactly

## Examples

### Complete Example

```go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"

    "github.com/mihaimyh/goquota/pkg/billing"
    "github.com/mihaimyh/goquota/pkg/billing/revenuecat"
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

func main() {
    // 1. Create storage
    storage := memory.New()

    // 2. Create goquota manager
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

    // 3. Create RevenueCat provider
    provider, err := revenuecat.NewProvider(billing.Config{
        Manager: manager,
        TierMapping: map[string]string{
            "scholar_monthly": "scholar",
            "scholar_annual":  "scholar",
            "fluent_monthly":  "fluent",
            "fluent_annual":   "fluent",
            "*":               "explorer", // Default tier
        },
        Secret: os.Getenv("REVENUECAT_SECRET"),
    })
    if err != nil {
        log.Fatal(err)
    }

    // 4. Register webhook endpoint
    http.Handle("/webhooks/revenuecat", provider.WebhookHandler())

    // 5. Register restore purchases endpoint
    http.HandleFunc("/restore-purchases", func(w http.ResponseWriter, r *http.Request) {
        userID := r.URL.Query().Get("user_id")
        if userID == "" {
            http.Error(w, "user_id required", http.StatusBadRequest)
            return
        }

        tier, err := provider.SyncUser(r.Context(), userID)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{
            "user_id": userID,
            "tier":    tier,
        })
    })

    // 6. Start server
    log.Println("Server starting on :8080")
    log.Println("Webhook: http://localhost:8080/webhooks/revenuecat")
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
    Timeout: 30 * time.Second,
    Transport: otelhttp.NewTransport(http.DefaultTransport),
}

provider, _ := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: tierMapping,
    Secret: secret,
    HTTPClient: httpClient,
})
```

### With HMAC Verification

```go
provider, _ := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: tierMapping,
    Secret: os.Getenv("REVENUECAT_HMAC_SECRET"),
    EnableHMAC: true, // Enable HMAC signature verification
})
```

### With Multiple Tiers

```go
provider, _ := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{
        // Monthly subscriptions
        "basic_monthly":    "basic",
        "pro_monthly":      "pro",
        "enterprise_monthly": "enterprise",
        
        // Annual subscriptions
        "basic_annual":     "basic",
        "pro_annual":       "pro",
        "enterprise_annual": "enterprise",
        
        // Default
        "*":                "free",
    },
    Secret: secret,
})
```

## Troubleshooting

### Webhook Not Processing

**Symptoms**: Webhooks are received but entitlements aren't updating.

**Solutions**:
1. **Check authentication**: Verify `Secret` matches RevenueCat webhook secret
2. **Check logs**: Look for error messages in webhook handler
3. **Test event**: Send a `TEST` event from RevenueCat dashboard
4. **Check tier mapping**: Verify entitlement ID is in `TierMapping`

**Debug**:
```go
// Add logging to webhook handler
log.Printf("Webhook received: %s", r.URL.Path)
log.Printf("Headers: %v", r.Header)
```

### SyncUser Returns Default Tier

**Symptoms**: `SyncUser` always returns default tier even for paid users.

**Solutions**:
1. **Check user exists**: Verify user exists in RevenueCat dashboard
2. **Check active entitlements**: User's subscription may have expired
3. **Check API credentials**: Verify `Secret` is correct
4. **Check tier mapping**: Verify entitlement IDs match `TierMapping`

**Debug**:
```go
tier, err := provider.SyncUser(ctx, userID)
if err != nil {
    log.Printf("SyncUser error: %v", err)
}
log.Printf("Detected tier: %s", tier)
```

### Tier Not Updating After Purchase

**Symptoms**: User purchases subscription but tier doesn't change.

**Solutions**:
1. **Check webhook delivery**: Verify webhooks are being received
2. **Check event type**: Ensure `INITIAL_PURCHASE` events are enabled
3. **Check idempotency**: Older events may be ignored (check timestamps)
4. **Check manager**: Verify `Manager.SetEntitlement` is working

**Debug**:
```go
// Check entitlement after webhook
ent, err := manager.GetEntitlement(ctx, userID)
if err != nil {
    log.Printf("GetEntitlement error: %v", err)
} else {
    log.Printf("Current entitlement: tier=%s, updated_at=%v", ent.Tier, ent.UpdatedAt)
}
```

### Rate Limit Errors

**Symptoms**: Webhook returns `429 Too Many Requests`.

**Solutions**:
1. **Check request volume**: Verify you're not sending too many webhooks
2. **Check IP address**: Rate limiting is per IP address
3. **Wait**: Rate limit resets after 1 minute

**Note**: Rate limiting is intentional to prevent abuse. If you're hitting limits legitimately, consider:
- Using a load balancer with proper IP forwarding
- Contacting RevenueCat support for webhook batching

### Invalid Signature Errors

**Symptoms**: Webhook returns `401 Unauthorized`.

**Solutions**:
1. **Check secret**: Verify `Secret` matches RevenueCat webhook secret
2. **Check HMAC setting**: If using HMAC, ensure `EnableHMAC: true`
3. **Check header**: Verify `Authorization` or `X-RevenueCat-Signature` header is present

## Idempotency

The RevenueCat provider uses **timestamp-based idempotency** to handle duplicate and out-of-order webhook deliveries.

### How It Works

1. Each webhook event includes a `timestamp_ms` field (Unix timestamp in milliseconds)
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

## API Reference

### NewProvider

Creates a new RevenueCat billing provider.

```go
func NewProvider(config billing.Config) (*Provider, error)
```

**Parameters**:
- `config`: Billing configuration (see [Configuration](#configuration))

**Returns**:
- `*Provider`: The RevenueCat provider instance
- `error`: Configuration error if any

**Example**:
```go
provider, err := revenuecat.NewProvider(billing.Config{
    Manager: manager,
    TierMapping: map[string]string{"premium_monthly": "premium"},
    Secret: "rcsk_...",
})
```

### Name

Returns the provider name.

```go
func (p *Provider) Name() string
```

**Returns**: `"revenuecat"`

### WebhookHandler

Returns the HTTP handler for RevenueCat webhooks.

```go
func (p *Provider) WebhookHandler() http.Handler
```

**Returns**: HTTP handler with rate limiting and security features

**Example**:
```go
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())
```

### SyncUser

Synchronizes a user's entitlement from RevenueCat API.

```go
func (p *Provider) SyncUser(ctx context.Context, userID string) (string, error)
```

**Parameters**:
- `ctx`: Context for cancellation/timeout
- `userID`: RevenueCat app user ID

**Returns**:
- `string`: Detected tier (e.g., "scholar", "fluent", "explorer")
- `error`: Error if sync fails

**Example**:
```go
tier, err := provider.SyncUser(ctx, "user_123")
if err != nil {
    log.Printf("Sync failed: %v", err)
} else {
    log.Printf("User tier: %s", tier)
}
```

### MapEntitlementToTier

Maps a RevenueCat entitlement ID to a goquota tier.

```go
func (p *Provider) MapEntitlementToTier(entitlementID string) string
```

**Parameters**:
- `entitlementID`: RevenueCat entitlement/product ID

**Returns**: Mapped tier name

**Example**:
```go
tier := provider.MapEntitlementToTier("scholar_monthly")
// Returns: "scholar"
```

## Error Handling

### Error Types

```go
import "github.com/mihaimyh/goquota/pkg/billing"

// Configuration errors
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

### HTTP Status Codes

The webhook handler returns the following status codes:

- `200 OK`: Webhook processed successfully
- `400 Bad Request`: Invalid payload or missing required fields
- `401 Unauthorized`: Invalid signature or authentication failed
- `413 Request Entity Too Large`: Payload exceeds 256KB limit
- `429 Too Many Requests`: Rate limit exceeded
- `500 Internal Server Error`: Processing error

## Best Practices

### 1. Tier Mapping

- **Use explicit mappings** for all known entitlements
- **Always include a default** (`"*"` or `"default"`) for unknown entitlements
- **Keep mappings in sync** with RevenueCat products

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

```go
Secret: os.Getenv("REVENUECAT_SECRET"),
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

### 5. Monitoring

- **Track webhook volume**: Monitor request rates
- **Monitor sync operations**: Track `SyncUser` success/failure rates
- **Alert on errors**: Set up alerts for repeated failures

### 6. Grace Periods

RevenueCat may send events during grace periods (e.g., `BILLING_ISSUE`, `CANCELLATION`). The provider:
- **Keeps tier active** if expiration is in the future
- **Downgrades to default** if expiration is in the past

This ensures users retain access during grace periods but are downgraded after expiration.

## Migration Guide

### From Manual Webhook Handling

**Before**:
```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
    // Manual parsing, validation, entitlement updates
    var payload RevenueCatPayload
    json.NewDecoder(r.Body).Decode(&payload)
    // ... lots of manual code ...
    manager.SetEntitlement(ctx, entitlement)
}
```

**After**:
```go
provider, _ := revenuecat.NewProvider(config)
http.Handle("/webhooks/revenuecat", provider.WebhookHandler())
```

### From Firestore Event Deduplication

**Before**:
```go
// Using Firestore to track processed events
eventDoc := firestore.Collection("events").Doc(eventID)
exists, _ := eventDoc.Get(ctx)
if exists {
    return // Already processed
}
eventDoc.Set(ctx, map[string]interface{}{"processed": true})
```

**After**:
```go
// Automatic timestamp-based idempotency
// No external storage needed!
provider.WebhookHandler().ServeHTTP(w, r)
```

## See Also

- [Billing Provider Documentation](../README.md) - General billing provider interface
- [goquota Manager Documentation](../../goquota/README.md) - Core quota management
- [RevenueCat Webhook Documentation](https://docs.revenuecat.com/docs/webhooks) - Official RevenueCat docs

