package billing

import (
	"context"
	"net/http"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Config defines the standard configuration all providers should accept
type Config struct {
	// Manager is the goquota Manager instance that will be updated with entitlements
	Manager *goquota.Manager

	// TierMapping maps provider product/entitlement IDs to goquota tiers.
	// For example: map[string]string{"scholar_monthly": "scholar", "fluent_monthly": "fluent"}
	// Reserved keys:
	//   - "*" or "default": Maps unknown entitlements to the default tier
	TierMapping map[string]string

	// WebhookSecret is used to verify incoming webhook requests (e.g. RevenueCat
	// X-RevenueCat-Signature or Bearer tokens).
	WebhookSecret string

	// APIKey is used for outbound API calls to the billing provider (e.g. SyncUser).
	APIKey string

	// HTTPClient is an optional HTTP client for API calls.
	// If nil, a default client with 10s timeout will be used.
	// Allows custom timeouts, proxies, or instrumentation (e.g., OpenTelemetry).
	HTTPClient *http.Client

	// EnableHMAC enforces HMAC signature verification (if supported by provider).
	// When enabled, the provider will verify webhook signatures using HMAC-SHA256.
	// Defaults to false (uses Bearer token authentication).
	EnableHMAC bool

	// Metrics is an optional metrics collector for tracking billing provider operations.
	// If nil, metrics will be silently ignored (no-op).
	// Use billing/metrics/prometheus.DefaultMetrics(namespace) for Prometheus metrics.
	Metrics Metrics

	// WebhookCallback is invoked after successful webhook processing.
	// The callback receives information about the webhook event including user ID,
	// previous tier, new tier, provider name, event type, and metadata.
	//
	// IMPORTANT: The callback is executed AFTER the entitlement is committed to storage
	// but BEFORE the HTTP 200 response is sent. If the callback returns an error,
	// the webhook handler returns HTTP 500, triggering the provider's retry mechanism.
	//
	// CRITICAL: Due to timestamp-based idempotency, if the callback fails after the
	// entitlement is committed, webhook retries may NOT re-execute the callback.
	// This is "best-effort" execution. For critical syncs (e.g., Firebase Auth claims),
	// implement asynchronous reconciliation (e.g., periodic sync job).
	//
	// Use cases:
	//   - Update Firebase Auth custom claims for JWT authorization
	//   - Send notification emails
	//   - Track analytics events
	//   - Custom audit logging
	//
	// Example:
	//   WebhookCallback: func(ctx context.Context, event WebhookEvent) error {
	//       if event.NewTier != event.PreviousTier {
	//           return authClient.UpdateCustomClaims(ctx, event.UserID, map[string]interface{}{
	//               "tier": event.NewTier,
	//           })
	//       }
	//       return nil
	//   }
	//
	// Note: This callback only fires for webhook events. Direct calls to
	// manager.SetEntitlement() or manager.ApplyTierChange() will NOT trigger it.
	WebhookCallback func(context.Context, WebhookEvent) error
}
