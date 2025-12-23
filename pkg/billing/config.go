package billing

import (
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
}
