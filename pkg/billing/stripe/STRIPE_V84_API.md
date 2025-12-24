# Stripe Go SDK v84 API Documentation

This document describes how the Stripe Go SDK v84 is used in the `goquota` Stripe billing provider.

## Overview

The Stripe provider uses **stripe-go v84.1.0**, which introduces a breaking change in how billing period fields are accessed (moved from `Subscription` to `SubscriptionItem` in API version 2025-03-31.basil).

## Key Changes from v83 to v84

### Breaking Change: CurrentPeriodEnd/Start Location

**In v83 and earlier:**

- `CurrentPeriodEnd` and `CurrentPeriodStart` were on the `Subscription` struct (but not exposed in Go SDK)
- Required parsing raw JSON to access these fields

**In v84 (API version 2025-03-31.basil):**

- These fields were **moved** to the `SubscriptionItem` struct
- Now properly exposed in the Go SDK as struct fields
- Access pattern: `sub.Items.Data[i].CurrentPeriodEnd` instead of parsing JSON

### 1. Client-Based API (v82+)

**Old Pattern (v76):**

```go
import "github.com/stripe/stripe-go/v76"
import "github.com/stripe/stripe-go/v76/customer"
import "github.com/stripe/stripe-go/v76/subscription"

stripe.Key = "sk_test_..."
customer.Get("cus_123", nil)
subscription.Get("sub_123", nil)
```

**New Pattern (v83):**

```go
import "github.com/stripe/stripe-go/v83"

client := stripe.NewClient("sk_test_...")
client.V1Customers.Retrieve(ctx, "cus_123", nil)
client.V1Subscriptions.Retrieve(ctx, "sub_123", nil)
```

### 2. Webhook Event Construction

**Old Pattern:**

```go
import "github.com/stripe/stripe-go/v76/webhook"
event, err := webhook.ConstructEvent(payload, sig, secret)
```

**New Pattern:**

```go
import "github.com/stripe/stripe-go/v83"
event, err := stripe.ConstructEvent(payload, sig, secret)
```

## API Usage in This Project

### Client Initialization

The Stripe client is created in `NewProvider()`:

```go
// pkg/billing/stripe/provider.go
stripeClient := stripe.NewClient(apiKey)
```

The client is stored in the `Provider` struct:

```go
type Provider struct {
    // ...
    stripeClient *stripe.Client
    // ...
}
```

### Customer Operations

#### Search Customers by Metadata

Used in `sync.go` to find customers by `user_id` metadata:

```go
// pkg/billing/stripe/sync.go
func (p *Provider) searchCustomerByMetadata(ctx context.Context, userID string) (string, error) {
    params := &stripe.CustomerSearchParams{}
    params.Query = fmt.Sprintf("metadata['user_id']:'%s'", userID)

    // New v83 API: client.V1Customers.Search()
    for cust, err := range p.stripeClient.V1Customers.Search(ctx, params) {
        if err != nil {
            return "", fmt.Errorf("stripe search error: %w", err)
        }
        if cust.Metadata != nil && cust.Metadata["user_id"] == userID {
            return cust.ID, nil
        }
    }
    return "", billing.ErrUserNotFound
}
```

**Key Points:**

- Uses `stripe.CustomerSearchParams` for query parameters
- Returns an iterator that must be ranged over
- Each iteration returns `(customer, error)` tuple
- Search query uses Stripe's query syntax: `metadata['user_id']:'value'`

#### Retrieve Customer

Used to fetch customer metadata when subscription metadata is missing:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) extractUserIDFromSubscription(ctx context.Context, sub *stripe.Subscription) (string, error) {
    // ... check subscription metadata first ...

    // Fallback to customer metadata
    if sub.Customer != nil {
        // New v83 API: client.V1Customers.Retrieve()
        cust, err := p.stripeClient.V1Customers.Retrieve(ctx, sub.Customer.ID, nil)
        if err == nil && cust.Metadata != nil {
            if userID, ok := cust.Metadata["user_id"]; ok && userID != "" {
                return userID, nil
            }
        }
    }
    // ...
}
```

**Key Points:**

- Uses `V1Customers.Retrieve(ctx, customerID, params)`
- Returns `(*Customer, error)`
- `params` can be `nil` or `&stripe.CustomerRetrieveParams{}` for expansion

### Subscription Operations

#### List Active Subscriptions

Used in `sync.go` to fetch all active subscriptions for a customer:

```go
// pkg/billing/stripe/sync.go
func (p *Provider) syncCustomer(ctx context.Context, customerID, userID string, startTime time.Time) (string, error) {
    params := &stripe.SubscriptionListParams{}
    params.Customer = stripe.String(customerID)
    params.Status = stripe.String("active")

    var subscriptions []*stripe.Subscription

    // New v83 API: client.V1Subscriptions.List()
    for sub, err := range p.stripeClient.V1Subscriptions.List(ctx, params) {
        if err != nil {
            return p.defaultTier, fmt.Errorf("failed to list subscriptions: %w", err)
        }
        if sub.Status == "active" {
            subscriptions = append(subscriptions, sub)
        }
    }
    // ...
}
```

**Key Points:**

- Uses `stripe.SubscriptionListParams` with `Customer` and `Status` filters
- Returns an iterator that must be ranged over
- Each iteration returns `(subscription, error)` tuple
- Helper functions like `stripe.String()` are used for pointer fields

#### Retrieve Subscription

Used in webhook handlers to fetch full subscription details:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) handleInvoicePaymentSucceeded(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
    // ... extract subscription ID from invoice ...

    // New v83 API: client.V1Subscriptions.Retrieve()
    sub, err := p.stripeClient.V1Subscriptions.Retrieve(ctx, subscriptionID, nil)
    if err != nil {
        return fmt.Errorf("failed to fetch subscription: %w", err)
    }
    // ...
}
```

**Key Points:**

- Uses `V1Subscriptions.Retrieve(ctx, subscriptionID, params)`
- Returns `(*Subscription, error)`
- Used when webhook events only contain subscription IDs, not full objects

#### Update Subscription Metadata

Used in `checkout.session.completed` handler to patch missing metadata:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) handleCheckoutSessionCompleted(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
    // ... extract subscription ID ...

    sub, err := p.stripeClient.V1Subscriptions.Retrieve(ctx, subscriptionID, nil)
    if err != nil {
        return fmt.Errorf("failed to fetch subscription: %w", err)
    }

    if sub.Metadata == nil || sub.Metadata["user_id"] == "" {
        // New v83 API: client.V1Subscriptions.Update()
        params := &stripe.SubscriptionUpdateParams{}
        params.AddMetadata("user_id", userID)
        sub, err = p.stripeClient.V1Subscriptions.Update(ctx, subscriptionID, params)
        if err != nil {
            return fmt.Errorf("failed to patch subscription metadata: %w", err)
        }
    }
    // ...
}
```

**Key Points:**

- Uses `V1Subscriptions.Update(ctx, subscriptionID, params)`
- `params.AddMetadata(key, value)` adds metadata fields
- Returns updated `(*Subscription, error)`

### Webhook Event Processing

#### Event Construction

Webhook signature verification uses the direct package function:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) handleWebhook(w http.ResponseWriter, r *http.Request) {
    // ... read body ...

    // New v83 API: stripe.ConstructEvent() (not webhook.ConstructEvent)
    event, err := stripe.ConstructEvent(body, sig, string(p.webhookSecret))
    if err != nil {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    // ...
}
```

**Key Points:**

- Uses `stripe.ConstructEvent(payload, signature, secret)` directly
- No longer requires `webhook` subpackage import
- Returns `(*Event, error)`

#### Event Data Unmarshaling

Webhook events contain raw JSON that must be unmarshaled:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) handleSubscriptionCreated(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
    var subscription stripe.Subscription
    if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
        return fmt.Errorf("failed to unmarshal subscription: %w", err)
    }
    // ...
}
```

**Key Points:**

- `event.Data.Raw` contains the raw JSON bytes
- Must manually unmarshal into the appropriate struct type
- This is the standard pattern for all webhook event handlers

## Important Field Access Patterns

### Period Fields (CurrentPeriodEnd/Start)

**Critical:** In v83, `CurrentPeriodEnd` and `CurrentPeriodStart` are **not** directly available in the `Subscription` struct. They must be extracted from raw JSON:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) extractTierFromSubscription(sub *stripe.Subscription, rawJSON []byte) (string, *time.Time, *time.Time) {
    // Extract period dates from raw JSON if available
    var currentPeriodEnd, currentPeriodStart int64
    if len(rawJSON) > 0 {
        var rawData map[string]interface{}
        if err := json.Unmarshal(rawJSON, &rawData); err == nil {
            if end, ok := rawData["current_period_end"].(float64); ok {
                currentPeriodEnd = int64(end)
            }
            if start, ok := rawData["current_period_start"].(float64); ok {
                currentPeriodStart = int64(start)
            }
        }
    }

    // Use extracted values...
    if currentPeriodEnd > 0 {
        exp := time.Unix(currentPeriodEnd, 0)
        expiresAt = &exp
    }
    // ...
}
```

**Why This Pattern:**

- Stripe API v83 may not include these fields in the generated Go structs
- Raw JSON from webhook events always contains these fields
- This pattern ensures compatibility across API versions

### Invoice Subscription Field

Similar pattern for accessing subscription from invoice:

```go
// pkg/billing/stripe/webhook.go
func (p *Provider) handleInvoicePaymentSucceeded(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
    var invoice stripe.Invoice
    if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
        return fmt.Errorf("failed to unmarshal invoice: %w", err)
    }

    // Extract subscription ID from raw JSON
    var rawData map[string]interface{}
    subscriptionID := ""
    if err := json.Unmarshal(event.Data.Raw, &rawData); err == nil {
        if sub, ok := rawData["subscription"].(map[string]interface{}); ok {
            if id, ok := sub["id"].(string); ok {
                subscriptionID = id
            }
        } else if subID, ok := rawData["subscription"].(string); ok {
            // Sometimes subscription is just an ID string
            subscriptionID = subID
        }
    }
    // ...
}
```

**Key Points:**

- `Invoice.Subscription` may not be directly accessible in v83
- Extract from raw JSON to handle both object and string formats
- Then use `V1Subscriptions.Retrieve()` to get full subscription details

## Helper Functions

### Pointer Field Helpers

Stripe v83 uses pointer fields extensively. Use helper functions:

```go
// For string fields
params.Customer = stripe.String("cus_123")
params.Status = stripe.String("active")

// For int64 fields
params.Limit = stripe.Int64(10)

// For bool fields
params.AutoAdvance = stripe.Bool(true)
```

### Metadata Management

```go
params := &stripe.SubscriptionUpdateParams{}
params.AddMetadata("user_id", userID)
params.AddMetadata("order_id", "12345")
```

## Error Handling

All API calls return `(resource, error)` tuples:

```go
customer, err := p.stripeClient.V1Customers.Retrieve(ctx, customerID, nil)
if err != nil {
    // Handle error - could be network, API, or validation error
    return fmt.Errorf("failed to retrieve customer: %w", err)
}
// Use customer...
```

## Context Usage

All v83 API methods require a `context.Context` as the first parameter:

```go
ctx := context.Background()  // or from request
customer, err := p.stripeClient.V1Customers.Retrieve(ctx, customerID, nil)
```

This enables:

- Request cancellation
- Timeout handling
- Request tracing

## Iterator Pattern

List and Search operations return iterators:

```go
// Correct usage
for item, err := range p.stripeClient.V1Subscriptions.List(ctx, params) {
    if err != nil {
        // Handle iteration error
        return err
    }
    // Process item
}

// Incorrect - don't call .Next() or .Err() manually
// The range loop handles iteration automatically
```

## Testing Considerations

When testing with v83:

1. **Mock the client**: Create a test client or use httptest server
2. **Raw JSON**: Provide raw JSON in test events for period fields
3. **Context**: Always pass context in test calls

Example test pattern:

```go
// pkg/billing/stripe/provider_test.go
sub := &stripe.Subscription{
    ID:     "sub_test",
    Status: "active",
    Created: now.Unix(),
    // Note: CurrentPeriodStart/End not in v83 struct
}

// Create raw JSON with period dates for extraction
rawJSON := []byte(fmt.Sprintf(`{
    "id":"sub_test",
    "status":"active",
    "created":%d,
    "current_period_start":%d,
    "current_period_end":%d,
    "items":{"data":[{"price":{"id":"%s"}}]}
}`, now.Unix(), now.Unix(), now.AddDate(0, 1, 0).Unix(), testPriceIDPro))

tier, expiresAt, startDate := provider.extractTierFromSubscription(sub, rawJSON)
```

## Migration Checklist

If upgrading from v76:

- [ ] Replace `stripe.Key = ...` with `stripe.NewClient(...)`
- [ ] Update all imports from `v76` to `v83`
- [ ] Change `webhook.ConstructEvent()` to `stripe.ConstructEvent()`
- [ ] Update all API calls to use client methods (`client.V1Xxx.Yyy()`)
- [ ] Add `context.Context` as first parameter to all API calls
- [ ] Update iterator usage (use `range` instead of `.Next()`)
- [ ] Extract period fields from raw JSON instead of struct fields
- [ ] Update tests to use v83 structs and patterns

## References

- [Stripe Go SDK v83 Documentation](https://pkg.go.dev/github.com/stripe/stripe-go/v83)
- [Stripe API Reference](https://stripe.com/docs/api)
- [Migration Guide: Stripe Client](https://github.com/stripe/stripe-go/wiki/Migration-guide-for-Stripe-Client)
- [Migration Guide: v73](https://github.com/stripe/stripe-go/wiki/Migration-guide-for-v73) (webhook changes)

## Summary

The v83 API provides:

- ✅ Better isolation (no global state)
- ✅ Context support for cancellation/timeouts
- ✅ More consistent API patterns
- ✅ Better testability
- ⚠️ Requires extracting some fields from raw JSON
- ⚠️ Breaking changes from v76 require code updates

This project successfully uses v83 with workarounds for period field extraction, ensuring full compatibility with Stripe's latest API.
