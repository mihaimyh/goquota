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
}
