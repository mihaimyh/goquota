package billing

import (
	"context"
	"net/http"
)

// Provider is the generic interface that any billing backend must implement.
// This allows the application to swap Stripe for RevenueCat with zero logic changes.
type Provider interface {
	// Name returns the provider name (e.g., "revenuecat", "stripe")
	Name() string

	// WebhookHandler returns the HTTP handler that processes real-time events.
	// The implementation handles validation, parsing, and Manager updates internally.
	WebhookHandler() http.Handler

	// SyncUser forces a synchronization of the user's state from the provider
	// to the goquota Manager (and optional persistent storage).
	// This is used for "Restore Purchases" or nightly reconciliation jobs.
	// Returns the detected tier and any error.
	SyncUser(ctx context.Context, userID string) (string, error)

	// CheckoutURL generates a URL to redirect the user to payment.
	// The tier parameter is the internal tier name (e.g., "pro") which is automatically
	// resolved to the provider-specific identifier using the configured TierMapping.
	//
	// Parameters:
	//   - ctx: Request context
	//   - userID: Internal user identifier (will be stored in provider metadata)
	//   - tier: Internal tier name from your TierMapping (e.g., "pro", "premium")
	//   - successURL: URL to redirect after successful payment
	//   - cancelURL: URL to redirect if user cancels
	//
	// Returns the checkout URL or an error if the tier is not configured.
	CheckoutURL(ctx context.Context, userID, tier, successURL, cancelURL string) (string, error)

	// PortalURL generates a URL for the user to manage their subscription.
	// This allows users to update payment methods, cancel subscriptions, or view invoices.
	//
	// Parameters:
	//   - ctx: Request context
	//   - userID: Internal user identifier
	//   - returnURL: URL to redirect after portal session ends
	//
	// Returns the portal URL or an error if the user has no active subscription.
	PortalURL(ctx context.Context, userID, returnURL string) (string, error)
}
