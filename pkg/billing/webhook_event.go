package billing

import "time"

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

	// ExpiresAt is when the entitlement expires (nil for lifetime/unknown)
	ExpiresAt *time.Time

	// Metadata contains provider-specific additional data
	// Stripe: Contains subscription metadata (e.g., customer.metadata, subscription.metadata)
	// RevenueCat: Contains product_id, entitlement_id from the webhook payload
	Metadata map[string]interface{}
}
