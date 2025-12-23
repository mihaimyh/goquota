package revenuecat

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// webhookPayload represents the RevenueCat webhook payload structure
type webhookPayload struct {
	Event struct {
		ID               string   `json:"id"`
		Type             string   `json:"type"`
		AppUserID        string   `json:"app_user_id"`
		EntitlementID    string   `json:"entitlement_id"`
		EntitlementIDs   []string `json:"entitlement_ids"`
		ProductID        string   `json:"product_id"`
		ExpirationReason string   `json:"expiration_reason"`
		ExpirationAtMs   int64    `json:"expiration_at_ms"`
		TimestampMs      int64    `json:"timestamp_ms"`
		PurchaseDateMs   int64    `json:"purchase_date_ms"`
	} `json:"event"`

	Subscriber   entitlementContainer `json:"subscriber"`
	CustomerInfo entitlementContainer `json:"customer_info"`
}

type entitlementContainer struct {
	Entitlements map[string]entitlement `json:"entitlements"`
}

type entitlement struct {
	ProductIdentifier string    `json:"product_identifier"`
	IsActive          bool      `json:"is_active"`
	ExpiresDate       time.Time `json:"-"`
	ExpiresDateRaw    string    `json:"expires_date"`
}

func (e *entitlement) parseTimes() {
	if e == nil {
		return
	}
	if e.ExpiresDateRaw == "" {
		e.ExpiresDate = time.Time{}
		return
	}
	parsed, err := time.Parse(time.RFC3339, e.ExpiresDateRaw)
	if err != nil {
		// Try RFC3339Nano
		parsed, err = time.Parse(time.RFC3339Nano, e.ExpiresDateRaw)
		if err != nil {
			e.ExpiresDate = time.Time{}
			return
		}
	}
	e.ExpiresDate = parsed
}

func (p *webhookPayload) resolveEntitlement(entitlementID string) *entitlement {
	// First try the configured entitlement ID
	if ent := p.lookupEntitlement(entitlementID); ent != nil {
		return ent
	}

	// Fallback to event entitlement id if provided
	if strings.TrimSpace(entitlementID) == "" && strings.TrimSpace(p.Event.EntitlementID) != "" {
		return p.lookupEntitlement(strings.TrimSpace(p.Event.EntitlementID))
	}

	// Case-insensitive fallback
	if entitlementID != "" {
		for key, ent := range p.Subscriber.Entitlements {
			if strings.EqualFold(key, entitlementID) {
				ent.parseTimes()
				return &ent
			}
		}
		for key, ent := range p.CustomerInfo.Entitlements {
			if strings.EqualFold(key, entitlementID) {
				ent.parseTimes()
				return &ent
			}
		}
	}

	return nil
}

func (p *webhookPayload) lookupEntitlement(id string) *entitlement {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	if p.Subscriber.Entitlements != nil {
		if ent, ok := p.Subscriber.Entitlements[id]; ok {
			ent.parseTimes()
			return &ent
		}
	}
	if p.CustomerInfo.Entitlements != nil {
		if ent, ok := p.CustomerInfo.Entitlements[id]; ok {
			ent.parseTimes()
			return &ent
		}
	}
	return nil
}

// readBodyStrict reads the request body and validates it's not empty.
// Enforces a 256KB limit to prevent memory exhaustion attacks (DoS protection).
func readBodyStrict(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	// Limit payload to 256KB to prevent memory exhaustion attacks
	// RevenueCat webhook payloads are typically <100KB, so 256KB is a safe upper bound
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)

	body, err := io.ReadAll(r.Body)
	defer func() {
		if closeErr := r.Body.Close(); closeErr != nil {
			log.Printf("[WEBHOOK] provider=%s body_close=failed error=%v", providerName, closeErr)
		}
	}()
	if err != nil {
		// Check if error is due to body size limit
		if err.Error() == "http: request body too large" {
			return nil, fmt.Errorf("payload too large (max 256KB)")
		}
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	return body, nil
}

// extractTokenOrSignature extracts the authentication token or signature from the request
func extractTokenOrSignature(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[len("bearer "):])
	}
	if authHeader != "" {
		// Allow direct token (rare)
		return authHeader
	}
	sig := strings.TrimSpace(r.Header.Get("X-RevenueCat-Signature"))
	if sig == "" {
		sig = strings.TrimSpace(r.Header.Get("x-revenuecat-signature"))
	}
	return sig
}

// verifyRequest verifies the webhook request signature or token
func (p *Provider) verifyRequest(tokenOrSig string, body []byte) bool {
	if len(p.secret) == 0 {
		return false
	}
	if strings.TrimSpace(tokenOrSig) == "" {
		return false
	}

	// Primary: token match (RevenueCat common setup)
	if subtle.ConstantTimeCompare([]byte(tokenOrSig), p.secret) == 1 {
		return true
	}

	// Optional: HMAC signature verification (legacy/alternative)
	if !p.acceptHMAC {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(tokenOrSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, p.secret)
	if _, err := mac.Write(body); err != nil {
		return false
	}
	computed := mac.Sum(nil)
	return hmac.Equal(expected, computed)
}

// parseWebhookPayload parses the webhook JSON payload with strict validation
func parseWebhookPayload(body []byte, payload *webhookPayload) error {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(payload); err != nil {
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("multiple JSON objects in payload")
	}
	return nil
}

// parseEventTimestamp converts a millisecond timestamp to time.Time
func parseEventTimestamp(timestampMs int64) time.Time {
	if timestampMs <= 0 {
		return time.Time{}
	}
	return time.Unix(0, timestampMs*int64(time.Millisecond)).UTC()
}

// extractTierFromPayload extracts tier information from the webhook payload
func (p *Provider) extractTierFromPayload(payload *webhookPayload) (
	tier string, expiresAt *time.Time, productID, entitlementID string,
) {
	eventType := strings.TrimSpace(payload.Event.Type)
	productID = strings.TrimSpace(payload.Event.ProductID)

	// Handle negative event types with expiration semantics
	switch eventType {
	case "EXPIRATION", "CANCELLATION", "BILLING_ISSUE", "SUBSCRIPTION_PAUSED":
		return p.handleExpirationLike(payload)
	}

	// Check entitlement IDs in event array
	for _, candidate := range payload.Event.EntitlementIDs {
		if tier := p.MapEntitlementToTier(candidate); tier != p.defaultTier {
			return p.handleEntitlementFromEvent(payload, candidate)
		}
	}

	// Check event entitlement ID
	if payload.Event.EntitlementID != "" {
		if tier := p.MapEntitlementToTier(payload.Event.EntitlementID); tier != p.defaultTier {
			return p.handleEntitlementFromEvent(payload, payload.Event.EntitlementID)
		}
	}

	// Fallback to subscriber entitlements
	for candidate := range p.tierMapping {
		if candidate == "*" || candidate == "default" {
			continue
		}
		ent := payload.resolveEntitlement(candidate)
		if ent != nil && ent.IsActive {
			return p.handleEntitlementFromDetails(ent, candidate)
		}
	}

	// Default: return default tier
	exp := calculateDefaultExpiration(productID)
	return p.defaultTier, &exp, productID, ""
}

// handleEntitlementFromEvent processes an entitlement from the event array
func (p *Provider) handleEntitlementFromEvent(
	payload *webhookPayload, entitlementID string,
) (tier string, expiresAt *time.Time, productID, chosenEntitlementID string) {
	productID = strings.TrimSpace(payload.Event.ProductID)
	chosenEntitlementID = entitlementID
	tier = p.MapEntitlementToTier(entitlementID)

	if payload.Event.ExpirationAtMs > 0 {
		exp := parseEventTimestamp(payload.Event.ExpirationAtMs)
		expiresAt = &exp
	} else if ent := payload.resolveEntitlement(entitlementID); ent != nil && !ent.ExpiresDate.IsZero() {
		exp := ent.ExpiresDate
		expiresAt = &exp
	} else {
		exp := calculateDefaultExpiration(productID)
		expiresAt = &exp
	}

	return tier, expiresAt, productID, chosenEntitlementID
}

// handleExpirationLike handles expiration-like events
func (p *Provider) handleExpirationLike(
	payload *webhookPayload,
) (tier string, expiresAt *time.Time, productID, entitlementID string) {
	productID = strings.TrimSpace(payload.Event.ProductID)
	if payload.Event.ExpirationAtMs <= 0 {
		return p.defaultTier, nil, productID, ""
	}
	exp := parseEventTimestamp(payload.Event.ExpirationAtMs)
	now := time.Now()
	if exp.After(now) {
		// During grace period: try to find the best tier
		for _, candidate := range payload.Event.EntitlementIDs {
			if tier := p.MapEntitlementToTier(candidate); tier != p.defaultTier {
				return tier, &exp, productID, candidate
			}
		}
		// If no entitlement found, assume default tier
		return p.defaultTier, &exp, productID, ""
	}
	return p.defaultTier, &exp, productID, ""
}

// handleEntitlementFromDetails processes an entitlement from subscriber details
func (p *Provider) handleEntitlementFromDetails(
	ent *entitlement, entitlementID string,
) (tier string, expiresAt *time.Time, productID, chosenEntitlementID string) {
	productID = strings.TrimSpace(ent.ProductIdentifier)
	chosenEntitlementID = entitlementID
	tier = p.MapEntitlementToTier(entitlementID)

	if !ent.ExpiresDate.IsZero() {
		exp := ent.ExpiresDate
		expiresAt = &exp
	}

	if ent.IsActive {
		return tier, expiresAt, productID, chosenEntitlementID
	}

	// Inactive entitlement: treat as default tier
	return p.defaultTier, expiresAt, productID, chosenEntitlementID
}

// calculateDefaultExpiration calculates a default expiration based on product ID
func calculateDefaultExpiration(productID string) time.Time {
	now := time.Now()
	id := strings.ToLower(productID)
	switch {
	case strings.Contains(id, "annual") || strings.Contains(id, "year"):
		return now.AddDate(1, 0, 0)
	case strings.Contains(id, "monthly") || strings.Contains(id, "month"):
		return now.AddDate(0, 1, 0)
	case strings.Contains(id, "weekly") || strings.Contains(id, "week"):
		return now.AddDate(0, 0, 7)
	default:
		return now.AddDate(0, 1, 0) // Default to monthly
	}
}
