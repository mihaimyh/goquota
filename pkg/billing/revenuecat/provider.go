package revenuecat

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/billing/internal"
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
	manager       *goquota.Manager
	config        billing.Config
	httpClient    *http.Client
	rateLimiter   *internal.RateLimiter
	tierMapping   map[string]string
	defaultTier   string
	webhookSecret []byte
	apiKey        string
	acceptHMAC    bool
	metrics       billing.Metrics
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

	// Setup secrets / API keys
	webhookSecretStr := strings.TrimSpace(config.WebhookSecret)
	if strings.HasPrefix(strings.ToLower(webhookSecretStr), "bearer ") {
		webhookSecretStr = strings.TrimSpace(webhookSecretStr[len("bearer "):])
	}
	webhookSecret := []byte(webhookSecretStr)

	apiKey := strings.TrimSpace(config.APIKey)

	// Allow API key to be provided as a Bearer token and strip the prefix.
	if strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
		apiKey = strings.TrimSpace(apiKey[len("bearer "):])
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
	limiter := internal.NewRateLimiter(defaultRateLimitRequests, defaultRateLimitWindow)
	// Note: Cleanup is now handled lazily in the rate limiter's allow() method
	// to avoid resource leaks from unstoppable goroutines

	// Setup metrics (optional)
	metrics := config.Metrics
	if metrics == nil {
		metrics = &billing.NoopMetrics{}
	}

	return &Provider{
		manager:       config.Manager,
		config:        config,
		httpClient:    httpClient,
		rateLimiter:   limiter,
		tierMapping:   tierMapping,
		defaultTier:   defaultTier,
		webhookSecret: webhookSecret,
		apiKey:        apiKey,
		acceptHMAC:    config.EnableHMAC,
		metrics:       metrics,
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
	startTime := time.Now()
	setSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(p.webhookSecret) == 0 {
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
			p.metrics.RecordWebhookError(providerName, "payload_too_large")
		} else {
			http.Error(w, fmt.Sprintf("invalid payload: %v", err), http.StatusBadRequest)
			p.metrics.RecordWebhookError(providerName, "invalid_payload")
		}
		return
	}

	// Verify request signature
	tokenOrSig := extractTokenOrSignature(r)
	if !p.verifyRequest(tokenOrSig, body) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		p.metrics.RecordWebhookError(providerName, "auth_failed")
		return
	}

	// Parse webhook payload
	var payload webhookPayload
	if err := parseWebhookPayload(body, &payload); err != nil {
		// Provide more detailed error for debugging (but don't expose sensitive data)
		http.Error(w, fmt.Sprintf("invalid payload: %v", err), http.StatusBadRequest)
		p.metrics.RecordWebhookError(providerName, "invalid_payload")
		return
	}

	userID := strings.TrimSpace(payload.Event.AppUserID)
	if userID == "" {
		http.Error(w, "missing user id", http.StatusBadRequest)
		p.metrics.RecordWebhookError(providerName, "missing_user_id")
		return
	}

	eventType := strings.TrimSpace(payload.Event.Type)
	if eventType == "" {
		eventType = "UNKNOWN"
	}

	// Handle TEST events
	if strings.EqualFold(eventType, "TEST") {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			return
		}
		p.metrics.RecordWebhookEvent(providerName, "TEST", "success")
		p.metrics.RecordWebhookProcessingDuration(providerName, "TEST", time.Since(startTime))
		return
	}

	// Process webhook event
	if err := p.processWebhookEvent(r.Context(), &payload, userID); err != nil {
		http.Error(w, "failed to process webhook", http.StatusInternalServerError)
		p.metrics.RecordWebhookEvent(providerName, eventType, "error")
		p.metrics.RecordWebhookError(providerName, "processing_error")
		p.metrics.RecordWebhookProcessingDuration(providerName, eventType, time.Since(startTime))
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		return
	}

	p.metrics.RecordWebhookEvent(providerName, eventType, "success")
	p.metrics.RecordWebhookProcessingDuration(providerName, eventType, time.Since(startTime))
}

// processWebhookEvent processes a webhook event with timestamp-based idempotency
//
//nolint:gocyclo // Complex business logic with multiple validation and processing paths
func (p *Provider) processWebhookEvent(ctx context.Context, payload *webhookPayload, userID string) error {
	// Parse event timestamp (supports both timestamp_ms and event_timestamp_ms)
	eventTimestamp := parseEventTimestamp(payload.getEventTimestamp())

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

	// Record tier change metric
	if tierChanged {
		p.metrics.RecordTierChange(providerName, previousTier, effectiveTier)
	}

	// Set subscription start date
	subscriptionStartDate := time.Time{}
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if effectiveTier != p.defaultTier {
		// First time entering a paid tier
		// Supports both purchase_date_ms and purchased_at_ms field names
		if purchaseAt := parseEventTimestamp(payload.getPurchaseTimestamp()); !purchaseAt.IsZero() {
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
