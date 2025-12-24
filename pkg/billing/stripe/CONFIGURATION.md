# Stripe Provider Configuration Guide

This document describes the operational requirements and configuration steps needed to use the Stripe billing provider with goquota.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Metadata Configuration](#metadata-configuration)
- [Webhook Configuration](#webhook-configuration)
- [Known Behaviors](#known-behaviors)
- [Testing](#testing)

## Prerequisites

1. **Stripe Account**: Active Stripe account with API access
2. **API Keys**:
   - Secret API Key (starts with `sk_`)
   - Webhook Secret (starts with `whsec_`)
3. **Products & Prices**: Configured in Stripe Dashboard

## Metadata Configuration

### ⚠️ CRITICAL: User ID Metadata

The Stripe provider **requires** `metadata["user_id"]` to map Stripe customers/subscriptions to your application users. Without this, the provider cannot function.

### Option 1: Checkout Sessions (Recommended)

When creating Stripe Checkout Sessions, **always** include the user ID in subscription metadata:

```javascript
// Client-side or Backend
const session = await stripe.checkout.sessions.create({
  customer_email: user.email,
  subscription_data: {
    metadata: {
      user_id: "user_123", // ⚠️ CRITICAL - Your internal user ID
    },
  },
  line_items: [
    {
      price: "price_pro_monthly",
      quantity: 1,
    },
  ],
  mode: "subscription",
  success_url: "https://example.com/success",
  cancel_url: "https://example.com/cancel",
});
```

### Option 2: Direct Subscription Creation

If creating subscriptions directly via API:

```javascript
const subscription = await stripe.subscriptions.create({
  customer: "cus_123",
  items: [{ price: "price_pro_monthly" }],
  metadata: {
    user_id: "user_123", // ⚠️ CRITICAL
  },
});
```

### Option 3: Customer Metadata (Fallback)

You can also set metadata on the customer object:

```javascript
const customer = await stripe.customers.create({
  email: "user@example.com",
  metadata: {
    user_id: "user_123", // ⚠️ CRITICAL
  },
});
```

**Note**: Subscription metadata takes precedence over customer metadata. The provider checks subscription metadata first, then falls back to customer metadata.

## Webhook Configuration

### Required Webhook Events

Configure your Stripe webhook endpoint to receive the following events:

| Event Type                      | Purpose                                         |
| ------------------------------- | ----------------------------------------------- |
| `customer.subscription.created` | Initial subscription creation                   |
| `customer.subscription.updated` | Subscription changes (tier upgrades/downgrades) |
| `customer.subscription.deleted` | Subscription cancellation                       |
| `invoice.payment_succeeded`     | Renewal payments (updates expiration date)      |
| `invoice.payment_failed`        | Payment failures (logged for monitoring)        |
| `checkout.session.completed`    | Immediate entitlement after checkout            |

### Webhook Setup Steps

1. **Go to Stripe Dashboard** → Developers → Webhooks
2. **Click "Add endpoint"**
3. **Enter your endpoint URL**: `https://your-domain.com/webhooks/stripe`
4. **Select events to listen to**: Choose the 6 events listed above
5. **Copy the webhook signing secret** (starts with `whsec_`)
6. **Configure your application** with the webhook secret

### Webhook Security

The provider automatically verifies webhook signatures using `stripe.ConstructEvent()`. Ensure:

- Your webhook secret is kept secure
- HTTPS is used for your webhook endpoint
- The webhook secret is correctly configured in your application

## Known Behaviors

### SyncUser and Expiration Dates

**Behavior**: When using `SyncUser()` (manual sync via Stripe API), the entitlement may show as **Active with No Expiration Date**.

**Why This Happens**:

- The Stripe v83 SDK doesn't expose `current_period_end` as a struct field
- `SyncUser` uses the Stripe API directly (not webhooks), so it doesn't have access to raw JSON
- The function filters for `status='active'` subscriptions

**Why This is OK**:

- As long as Stripe reports the subscription as `active`, the user should have access
- The next webhook event (e.g., `invoice.payment_succeeded`) will populate the expiration date
- This is acceptable for production use

**Recommendation**:

- Use webhooks as the primary source of truth
- Use `SyncUser()` only for:
  - Initial user onboarding
  - Manual reconciliation
  - Debugging/support scenarios

### Checkout Session Metadata Patching

When `checkout.session.completed` is received:

1. The provider checks if the subscription has `metadata["user_id"]`
2. If missing, it patches the subscription with the user ID from the checkout session
3. The entitlement is **immediately** created (doesn't wait for `subscription.created` webhook)

This ensures users get instant access after checkout.

## Testing

### Test Mode Setup

1. Use test API keys (start with `sk_test_`)
2. Use test webhook secret (start with `whsec_test_`)
3. Create test products and prices in Stripe Dashboard

### Testing Webhooks Locally

Use Stripe CLI for local webhook testing:

```bash
# Install Stripe CLI
# https://stripe.com/docs/stripe-cli

# Forward webhooks to local server
stripe listen --forward-to localhost:8080/webhooks/stripe

# Trigger test events
stripe trigger customer.subscription.created
stripe trigger invoice.payment_succeeded
```

### Verifying Metadata

Check that metadata is correctly set:

```bash
# List subscriptions for a customer
stripe subscriptions list --customer cus_123

# Check subscription metadata
stripe subscriptions retrieve sub_123
```

Look for `"metadata": { "user_id": "user_123" }` in the response.

## Tier Mapping

Configure tier mapping in your application:

```go
provider, err := stripe.NewProvider(stripe.Config{
    Config: billing.Config{
        Manager: quotaManager,
        TierMapping: map[string]string{
            "price_basic_monthly":  "basic",
            "price_pro_monthly":    "pro",
            "price_enterprise":     "enterprise",
            "*":                    "explorer", // Default/fallback tier
        },
    },
    StripeAPIKey:        os.Getenv("STRIPE_API_KEY"),
    StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
})
```

### Tier Weights (Optional)

For users with multiple subscriptions, tier weights determine priority:

```go
provider, err := stripe.NewProvider(stripe.Config{
    // ... other config ...
    TierWeights: map[string]int{
        "enterprise": 100,
        "pro":        50,
        "basic":      10,
        "explorer":   0,  // Default tier
    },
})
```

Higher weight = higher priority. If not specified, weights are auto-assigned based on tier mapping order.

## Troubleshooting

### Issue: "metadata.user_id missing on subscription"

**Cause**: Subscription was created without user_id metadata

**Solution**:

1. Ensure checkout sessions include `subscription_data.metadata.user_id`
2. For existing subscriptions, update via Stripe Dashboard or API:
   ```bash
   stripe subscriptions update sub_123 --metadata user_id=user_123
   ```

### Issue: Webhook signature verification fails

**Cause**: Incorrect webhook secret or payload tampering

**Solution**:

1. Verify webhook secret matches Stripe Dashboard
2. Ensure webhook secret includes `whsec_` prefix
3. Check that payload hasn't been modified by middleware

### Issue: User has no expiration date after sync

**Cause**: Expected behavior (see [Known Behaviors](#known-behaviors))

**Solution**:

- Wait for next webhook event (e.g., invoice payment)
- Or manually trigger a webhook using Stripe CLI
- This is not a bug - the subscription is still active

## Production Checklist

Before going live:

- [ ] Webhook endpoint is publicly accessible via HTTPS
- [ ] All 6 required webhook events are configured
- [ ] Webhook secret is securely stored (environment variable)
- [ ] Stripe API key is securely stored (environment variable)
- [ ] Checkout sessions include `subscription_data.metadata.user_id`
- [ ] Tier mapping is correctly configured
- [ ] Test webhooks using Stripe CLI
- [ ] Verify entitlements are created correctly
- [ ] Monitor webhook delivery in Stripe Dashboard

## Support

For issues or questions:

- Check Stripe webhook logs in Dashboard → Developers → Webhooks
- Review application logs for webhook processing errors
- Use Stripe CLI to replay failed webhook events
- Consult the [Stripe API documentation](https://stripe.com/docs/api)
