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

	// Secret is the API key or webhook secret for authenticating with the provider
	Secret string

	// HTTPClient is an optional HTTP client for API calls.
	// If nil, a default client with 10s timeout will be used.
	// Allows custom timeouts, proxies, or instrumentation (e.g., OpenTelemetry).
	HTTPClient *http.Client

	// EnableHMAC enforces HMAC signature verification (if supported by provider).
	// When enabled, the provider will verify webhook signatures using HMAC-SHA256.
	// Defaults to false (uses Bearer token authentication).
	EnableHMAC bool
}
