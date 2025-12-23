package revenuecat

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

const (
	providerName             = "revenuecat"
	revenueCatAPIBaseURL     = "https://api.revenuecat.com/v1"
	defaultHTTPTimeout       = 10 * time.Second
	defaultRateLimitWindow   = time.Minute
	defaultRateLimitRequests = 100
	defaultTierName          = "explorer"
	defaultTierKeyWildcard   = "*"
	defaultTierKeyDefault    = "default"
)

// Provider implements the billing.Provider interface for RevenueCat
type Provider struct {
	manager     *goquota.Manager
	config      billing.Config
	httpClient  *http.Client
	rateLimiter *rateLimiter
	tierMapping map[string]string
	defaultTier string
	secret      []byte
	acceptHMAC  bool
}

// NewProvider creates a new RevenueCat billing provider
func NewProvider(config billing.Config) (*Provider, error) {
	if config.Manager == nil {
		return nil, billing.ErrProviderNotConfigured
	}

	// Setup HTTP client
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultHTTPTimeout,
		}
	}

	// Setup secret
	secret := []byte(strings.TrimSpace(config.Secret))
	secretStr := string(secret)
	if strings.HasPrefix(strings.ToLower(secretStr), "bearer ") {
		secret = []byte(strings.TrimSpace(secretStr[len("bearer "):]))
	}

	// Get default tier - use "explorer" as fallback
	// The manager will use its configured DefaultTier when no entitlement exists
	defaultTier := defaultTierName // Fallback default

	// Setup tier mapping
	tierMapping := make(map[string]string)
	for k, v := range config.TierMapping {
		tierMapping[strings.ToLower(k)] = v
	}

	// Check for default tier mapping
	if defaultTierKey, ok := tierMapping[defaultTierKeyWildcard]; ok {
		defaultTier = defaultTierKey
	} else if defaultTierKey, ok := tierMapping[defaultTierKeyDefault]; ok {
		defaultTier = defaultTierKey
	}

	// Setup rate limiter
	limiter := newRateLimiter(defaultRateLimitRequests, defaultRateLimitWindow)

	return &Provider{
		manager:     config.Manager,
		config:      config,
		httpClient:  httpClient,
		rateLimiter: limiter,
		tierMapping: tierMapping,
		defaultTier: defaultTier,
		secret:      secret,
		acceptHMAC:  config.EnableHMAC,
	}, nil
}

// Name returns the provider name
func (p *Provider) Name() string {
	return providerName
}

// WebhookHandler returns the HTTP handler for RevenueCat webhooks
func (p *Provider) WebhookHandler() http.Handler {
	handler := http.HandlerFunc(p.handleWebhook)
	// Wrap with rate limiting
	return p.rateLimiter.Middleware(handler)
}

// SyncUser synchronizes a user's entitlement from RevenueCat
func (p *Provider) SyncUser(ctx context.Context, userID string) (string, error) {
	return p.syncUserFromAPI(ctx, userID)
}

// GetDefaultTier returns the default tier for unknown entitlements
func (p *Provider) GetDefaultTier() string {
	return p.defaultTier
}

// MapEntitlementToTier maps a RevenueCat entitlement ID to a goquota tier
func (p *Provider) MapEntitlementToTier(entitlementID string) string {
	if entitlementID == "" {
		return p.defaultTier
	}

	// Try exact match (case-insensitive)
	key := strings.ToLower(strings.TrimSpace(entitlementID))
	if tier, ok := p.tierMapping[key]; ok {
		return tier
	}

	// Try partial match (contains)
	for mappingKey, tier := range p.tierMapping {
		if mappingKey == defaultTierKeyWildcard || mappingKey == defaultTierKeyDefault {
			continue // Skip default keys
		}
		if strings.Contains(key, mappingKey) || strings.Contains(mappingKey, key) {
			return tier
		}
	}

	// Fallback to default tier
	return p.defaultTier
}

// handleWebhook processes incoming RevenueCat webhook events
func (p *Provider) handleWebhook(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(p.secret) == 0 {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}

	select {
	case <-r.Context().Done():
		http.Error(w, "request timeout", http.StatusRequestTimeout)
		return
	default:
	}

	// Read and validate body (with size limit protection)
	body, err := readBodyStrict(w, r)
	if err != nil {
		if strings.Contains(err.Error(), "too large") {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid payload", http.StatusBadRequest)
		}
		return
	}

	// Verify request signature
	tokenOrSig := extractTokenOrSignature(r)
	if !p.verifyRequest(tokenOrSig, body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse webhook payload
	var payload webhookPayload
	if err := parseWebhookPayload(body, &payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(payload.Event.AppUserID)
	if userID == "" {
		http.Error(w, "missing user id", http.StatusBadRequest)
		return
	}

	// Handle TEST events
	if strings.EqualFold(strings.TrimSpace(payload.Event.Type), "TEST") {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			return
		}
		return
	}

	// Process webhook event
	if err := p.processWebhookEvent(r.Context(), &payload, userID); err != nil {
		http.Error(w, "failed to process webhook", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		return
	}
}

// processWebhookEvent processes a webhook event with timestamp-based idempotency
func (p *Provider) processWebhookEvent(ctx context.Context, payload *webhookPayload, userID string) error {
	// Parse event timestamp
	eventTimestamp := parseEventTimestamp(payload.Event.TimestampMs)

	// Extract tier information from payload
	tier, expiresAt, _, _ := p.extractTierFromPayload(payload)
	effectiveTier := tier

	// Check if entitlement has expired
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		effectiveTier = p.defaultTier
	}

	// Get existing entitlement for timestamp comparison (idempotency check)
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}

	// Timestamp-based idempotency: only update if event is newer
	if existing != nil && !eventTimestamp.After(existing.UpdatedAt) {
		// Event is older or duplicate - skip silently
		return nil
	}

	// Determine if tier changed
	previousTier := p.defaultTier
	if existing != nil {
		previousTier = existing.Tier
	}
	tierChanged := previousTier != effectiveTier

	// Set subscription start date
	subscriptionStartDate := time.Time{}
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if effectiveTier != p.defaultTier {
		// First time entering a paid tier
		if purchaseAt := parseEventTimestamp(payload.Event.PurchaseDateMs); !purchaseAt.IsZero() {
			subscriptionStartDate = startOfDayUTC(purchaseAt)
		} else {
			subscriptionStartDate = startOfDayUTC(time.Now().UTC())
		}
	}

	// Create entitlement with event timestamp (not time.Now())
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  effectiveTier,
		SubscriptionStartDate: subscriptionStartDate,
		UpdatedAt:             eventTimestamp, // Critical: use event timestamp, not time.Now()
	}

	if expiresAt != nil {
		ent.ExpiresAt = expiresAt
	}

	// Update entitlement in manager
	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	// Apply tier change if needed (with prorated quota adjustment)
	if tierChanged {
		// Apply tier change for all resources (or a specific resource if configured)
		// For now, we'll apply to a common resource - this can be made configurable
		applyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Note: ApplyTierChange requires a resource name. For now, we'll skip this
		// if no resource is specified, or make it configurable in the future.
		// The tier change will be reflected on next quota check anyway.
		_ = applyCtx
		_ = previousTier
		_ = effectiveTier
		// TODO: Apply tier change when resource is specified in config
	}

	return nil
}

// Helper functions

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func startOfDayUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
