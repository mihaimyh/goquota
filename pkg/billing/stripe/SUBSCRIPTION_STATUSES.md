# Stripe Subscription Status Handling

## Overview

The Stripe provider now supports multiple subscription statuses that grant tier access, not just "active" subscriptions.

## Supported Statuses

The provider grants tier access for the following subscription statuses:

1. **`active`** - Subscription is active and paid
2. **`trialing`** - Subscription is in trial period (users should get tier benefits during trial)
3. **`past_due`** - Payment failed but subscription is still in grace period (common practice to grant access during grace period)

## Excluded Statuses

The following statuses do **not** grant tier access:

- **`incomplete`** - Payment not yet confirmed (user hasn't paid)
- **`incomplete_expired`** - Payment attempt expired (user didn't complete payment)
- **`canceled`** - Subscription has been canceled
- **`unpaid`** - Payment failed and subscription is unpaid
- **`paused`** - Subscription is paused (if supported by your Stripe account)

## Implementation

The `isSubscriptionStatusValidForAccess()` function centralizes the logic for determining which subscription statuses should grant access. This function is used in:

1. **Webhook processing** (`webhook.go`) - When processing subscription events
2. **User sync** (`sync.go`) - When synchronizing user entitlements from Stripe API

## Benefits

1. **Trial Support**: Users on trial subscriptions now get their tier benefits immediately
2. **Grace Period**: Users with past_due subscriptions maintain access during the grace period (common SaaS practice)
3. **Better UX**: Users don't lose access immediately when a payment fails, giving them time to update payment methods

## Migration Notes

This change is **backward compatible**. Existing "active" subscriptions continue to work as before. The change only adds support for additional statuses.

If you need to customize which statuses grant access, you can modify the `isSubscriptionStatusValidForAccess()` function in `provider.go`.

