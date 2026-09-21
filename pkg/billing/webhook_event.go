package billing

import "time"

// Billing period classifications returned by DeriveBillingPeriod.
const (
	PeriodAnnual  = "annual"
	PeriodMonthly = "monthly"
)

// WebhookEvent contains information about a successful webhook processing event.
// This event is passed to the WebhookCallback after the entitlement has been
// successfully updated in storage.
type WebhookEvent struct {
	// UserID is the internal user identifier
	UserID string

	// PreviousTier is the tier before the webhook update (empty string if new user)
	PreviousTier string

	// NewTier is the tier after the webhook update
	NewTier string

	// Provider is the billing provider name ("stripe", "revenuecat")
	Provider string

	// EventType is the provider-specific event type
	// Stripe: "customer.subscription.created", "invoice.payment_succeeded", etc.
	// RevenueCat: "INITIAL_PURCHASE", "RENEWAL", "CANCELLATION", etc.
	EventType string

	// EventTimestamp is when the event occurred (from provider)
	EventTimestamp time.Time

	// EventID is the provider's unique event identifier (RevenueCat event.id,
	// Stripe evt_...). Use it for your own deduplication/audit trail.
	EventID string

	// ProductID is the store product / price identifier the event concerns.
	ProductID string

	// Store is the purchase store (RevenueCat: PLAY_STORE/APP_STORE, Stripe: "stripe").
	Store string

	// Currency is the ISO-4217 currency code for PriceCents.
	Currency string

	// PriceCents is the purchased amount in minor units (4999 == 49.99).
	// Zero when the event carries no price (cancellations, expirations).
	PriceCents int64

	// PeriodType is the provider billing period type
	// (RevenueCat: NORMAL, TRIAL, INTRO).
	PeriodType string

	// PurchasedAt is when the current billing period started, nil when unknown.
	PurchasedAt *time.Time

	// ExpiresAt is when the entitlement expires (nil for lifetime/unknown)
	ExpiresAt *time.Time

	// CancelAtPeriodEnd reports auto-renew state. True when the provider says the
	// subscription will not renew (e.g. RevenueCat CANCELLATION), false when it
	// will (renewal/purchase), and nil when the event does not speak to
	// cancellation — consumers must not clear stored state on nil.
	CancelAtPeriodEnd *bool

	// Metadata contains provider-specific additional data
	// Stripe: Contains subscription metadata (e.g., customer.metadata, subscription.metadata)
	// RevenueCat: Contains product_id, entitlement_id from the webhook payload
	Metadata map[string]interface{}
}

// DeriveBillingPeriod classifies a subscription interval as PeriodAnnual,
// PeriodMonthly, or "" when it cannot be determined (trials, weekly plans,
// lifetime access, or missing/again invalid timestamps).
func DeriveBillingPeriod(purchasedAt, expiresAt time.Time) string {
	if purchasedAt.IsZero() || expiresAt.IsZero() || !expiresAt.After(purchasedAt) {
		return ""
	}
	days := expiresAt.Sub(purchasedAt).Hours() / 24
	switch {
	case days >= 180:
		return PeriodAnnual
	case days >= 20:
		return PeriodMonthly
	default:
		return ""
	}
}
