package stripe

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/billing/internal"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

const (
	providerName               = "stripe"
	defaultHTTPTimeout         = 10 * time.Second
	defaultRateLimitWindow     = time.Minute
	defaultRateLimitRequests   = 100
	defaultTierName            = "explorer"
	defaultTierKeyWildcard     = "*"
	defaultTierKeyDefault      = "default"
	subscriptionStatusActive   = "active"
	subscriptionStatusTrialing = "trialing"
	subscriptionStatusPastDue  = "past_due"
)

// isSubscriptionStatusValidForAccess returns true if a subscription status should grant tier access
// Valid statuses: "active", "trialing", "past_due" (grace period)
// Invalid statuses: "incomplete", "incomplete_expired", "canceled", "unpaid", "paused"
func isSubscriptionStatusValidForAccess(status string) bool {
	switch status {
	case subscriptionStatusActive, subscriptionStatusTrialing, subscriptionStatusPastDue:
		return true
	default:
		return false
	}
}

// Config extends billing.Config with Stripe-specific options
type Config struct {
	billing.Config // Base config (Manager, TierMapping, etc.)

	// Stripe-specific
	StripeAPIKey        string
	StripeWebhookSecret string

	// Performance Hook (Optional)
	// If provided, SyncUser uses this for O(1) customer lookup
	// If nil, falls back to slow Stripe Search API
	CustomerIDResolver func(context.Context, string) (string, error)

	// Tier Weights (Optional)
	// Maps tier name -> priority weight (higher = better)
	// If nil, auto-assigns weights based on TierMapping order
	TierWeights map[string]int
}

// Provider implements the billing.Provider interface for Stripe
type Provider struct {
	manager            *goquota.Manager
	config             Config
	httpClient         *http.Client
	rateLimiter        *internal.RateLimiter
	tierMapping        map[string]string // Price/Product ID -> Tier
	tierWeights        map[string]int    // Tier -> Weight (for priority)
	defaultTier        string
	webhookSecret      []byte
	apiKey             string
	stripeClient       *stripe.Client
	customerIDResolver func(context.Context, string) (string, error)
	metrics            billing.Metrics
}

// validateProviderConfig validates the provider configuration
func validateProviderConfig(config Config) (apiKey, webhookSecretStr string, err error) {
	if config.Manager == nil {
		return "", "", billing.ErrProviderNotConfigured
	}

	// At least one of API key or webhook secret must be provided
	apiKey = strings.TrimSpace(config.StripeAPIKey)
	webhookSecretStr = strings.TrimSpace(config.StripeWebhookSecret)
	if apiKey == "" && webhookSecretStr == "" {
		return "", "", billing.ErrProviderNotConfigured
	}

	return apiKey, webhookSecretStr, nil
}

// setupTierMapping processes the tier mapping configuration and returns the mapping and default tier
func setupTierMapping(config Config) (tierMapping map[string]string, defaultTier string) {
	tierMapping = make(map[string]string)
	for k, v := range config.TierMapping {
		tierMapping[strings.ToLower(k)] = v
	}

	// Get default tier - use manager's default if available
	defaultTier = defaultTierName
	// Manager config may have DefaultTier, but we'll use our constant for consistency
	// The manager will use its configured DefaultTier when no entitlement exists

	// Check for default tier mapping
	if defaultTierKey, ok := tierMapping[defaultTierKeyWildcard]; ok {
		defaultTier = defaultTierKey
	} else if defaultTierKey, ok := tierMapping[defaultTierKeyDefault]; ok {
		defaultTier = defaultTierKey
	}

	return tierMapping, defaultTier
}

// setupTierWeights processes the tier weights configuration
func setupTierWeights(config Config, defaultTier string) map[string]int {
	tierWeights := make(map[string]int)
	if config.TierWeights != nil {
		// Use provided weights
		for tier, weight := range config.TierWeights {
			tierWeights[tier] = weight
		}
	} else {
		// Auto-assign weights based on tier mapping order
		// First tier = 100, second = 90, etc.
		weight := 100
		seenTiers := make(map[string]bool)
		for _, tier := range config.TierMapping {
			if tier != defaultTier && !seenTiers[tier] {
				tierWeights[tier] = weight
				seenTiers[tier] = true
				weight -= 10
				if weight < 0 {
					weight = 0
				}
			}
		}
	}
	// Default tier always has weight 0
	tierWeights[defaultTier] = 0
	return tierWeights
}

// NewProvider creates a new Stripe billing provider
func NewProvider(config Config) (*Provider, error) {
	apiKey, webhookSecretStr, err := validateProviderConfig(config)
	if err != nil {
		return nil, err
	}

	// Setup HTTP client
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultHTTPTimeout,
		}
	}

	// Setup Stripe API key and client
	// API key is optional - webhooks can work with just webhook secret
	// API key is only needed for operations that call Stripe API (SyncUser, subscription metadata updates)
	var stripeClient *stripe.Client
	if apiKey != "" {
		// Create Stripe client (new API in v82+)
		stripeClient = stripe.NewClient(apiKey)
	}

	// Setup webhook secret
	if strings.HasPrefix(strings.ToLower(webhookSecretStr), "whsec_") {
		webhookSecretStr = strings.TrimSpace(webhookSecretStr)
	}
	webhookSecret := []byte(webhookSecretStr)

	// Setup tier mapping and default tier
	tierMapping, defaultTier := setupTierMapping(config)

	// Setup tier weights
	tierWeights := setupTierWeights(config, defaultTier)

	// Setup rate limiter
	limiter := internal.NewRateLimiter(defaultRateLimitRequests, defaultRateLimitWindow)
	// Note: Cleanup is now handled lazily in the rate limiter's allow() method
	// to avoid resource leaks from unstoppable goroutines

	// Setup metrics (optional)
	metrics := config.Metrics
	if metrics == nil {
		metrics = &billing.NoopMetrics{}
	}

	return &Provider{
		manager:            config.Manager,
		config:             config,
		httpClient:         httpClient,
		rateLimiter:        limiter,
		tierMapping:        tierMapping,
		tierWeights:        tierWeights,
		defaultTier:        defaultTier,
		webhookSecret:      webhookSecret,
		apiKey:             apiKey,
		stripeClient:       stripeClient,
		customerIDResolver: config.CustomerIDResolver,
		metrics:            metrics,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return providerName
}

// WebhookHandler returns the HTTP handler for Stripe webhooks
func (p *Provider) WebhookHandler() http.Handler {
	handler := http.HandlerFunc(p.handleWebhook)
	// Wrap with rate limiting
	return p.rateLimiter.Middleware(handler)
}

// SyncUser synchronizes a user's entitlement from Stripe
func (p *Provider) SyncUser(ctx context.Context, userID string) (string, error) {
	return p.syncUserFromAPI(ctx, userID)
}

// GetDefaultTier returns the default tier for unknown entitlements
func (p *Provider) GetDefaultTier() string {
	return p.defaultTier
}

// MapPriceToTier maps a Stripe Price ID or Product ID to a goquota tier
func (p *Provider) MapPriceToTier(priceID string) string {
	if priceID == "" {
		return p.defaultTier
	}

	// Try exact match (case-insensitive)
	key := strings.ToLower(strings.TrimSpace(priceID))
	if tier, ok := p.tierMapping[key]; ok {
		return tier
	}

	// Fallback to default tier
	return p.defaultTier
}

// GetTierWeight returns the weight for a given tier
func (p *Provider) GetTierWeight(tier string) int {
	if weight, ok := p.tierWeights[tier]; ok {
		return weight
	}
	return 0 // Default weight
}
