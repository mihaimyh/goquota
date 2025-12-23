package revenuecat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// revenueCatSubscriberResponse represents the RevenueCat API subscriber response
type revenueCatSubscriberResponse struct {
	Subscriber revenueCatSubscriber `json:"subscriber"`
}

type revenueCatSubscriber struct {
	Entitlements map[string]revenueCatEntitlement `json:"entitlements"`
}

type revenueCatEntitlement struct {
	ExpiresDate       *string `json:"expires_date"`
	ProductIdentifier string  `json:"product_identifier"`
	PurchaseDate      *string `json:"purchase_date"`
}

// syncUserFromAPI synchronizes a user's entitlement from RevenueCat API
//
//nolint:gocyclo // Complex business logic with multiple error handling paths
func (p *Provider) syncUserFromAPI(ctx context.Context, userID string) (string, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return p.defaultTier, fmt.Errorf("revenuecat API key not configured")
	}

	// Build API URL
	url := fmt.Sprintf("%s/subscribers/%s", revenueCatAPIBaseURL, userID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return p.defaultTier, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")

	// Execute request
	res, err := p.httpClient.Do(req)
	if err != nil {
		return p.defaultTier, fmt.Errorf("failed to fetch subscriber: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return p.defaultTier, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle 404 - user not found in RevenueCat
	if res.StatusCode == http.StatusNotFound {
		// User doesn't exist in RevenueCat - set to default tier
		return p.syncToDefaultTier(ctx, userID)
	}

	// Handle other errors
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return p.defaultTier, fmt.Errorf("revenuecat API error: status %d, body: %s", res.StatusCode, string(body))
	}

	// Parse response
	var payload revenueCatSubscriberResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return p.defaultTier, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract tier from entitlements
	tier, expiresAt, purchaseAt, _, _ := p.extractTierFromSubscriber(&payload.Subscriber)

	// Get existing entitlement to check for tier changes
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return tier, err
	}

	previousTier := p.defaultTier
	tierChanged := false
	if existing != nil {
		previousTier = existing.Tier
		tierChanged = previousTier != tier
	}

	// Set subscription start date
	subscriptionStartDate := time.Time{}
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if tier != p.defaultTier && purchaseAt != nil {
		subscriptionStartDate = startOfDayUTC(purchaseAt.UTC())
	} else if tier != p.defaultTier {
		subscriptionStartDate = startOfDayUTC(time.Now().UTC())
	}

	// Create entitlement with current timestamp (sync operations use current time)
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: subscriptionStartDate,
		UpdatedAt:             time.Now().UTC(), // Sync uses current time
	}

	if expiresAt != nil {
		ent.ExpiresAt = expiresAt
	}

	// Update entitlement
	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return tier, fmt.Errorf("failed to set entitlement: %w", err)
	}

	// Apply tier change if needed
	if tierChanged {
		// Note: ApplyTierChange requires a resource name
		// For now, we'll skip this - the tier change will be reflected on next quota check
		// This can be made configurable in the future
		_ = previousTier
		_ = tier
	}

	return tier, nil
}

// syncToDefaultTier sets a user to the default tier (when not found in RevenueCat)
func (p *Provider) syncToDefaultTier(ctx context.Context, userID string) (string, error) {
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  p.defaultTier,
		SubscriptionStartDate: time.Time{},
		UpdatedAt:             time.Now().UTC(),
	}

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return p.defaultTier, fmt.Errorf("failed to set default tier: %w", err)
	}

	return p.defaultTier, nil
}

// extractTierFromSubscriber extracts tier information from RevenueCat subscriber data
//
//nolint:gocyclo // Complex business logic for tier extraction with multiple conditions
func (p *Provider) extractTierFromSubscriber(
	subscriber *revenueCatSubscriber,
) (tier string, expiresAt, purchaseAt *time.Time, productID, entitlementID string) {
	now := time.Now()

	// Priority list: check configured entitlements in order
	// Find the highest tier entitlement that is active
	for candidateID := range p.tierMapping {
		if candidateID == "*" || candidateID == "default" { //nolint:goconst // These are magic strings for tier mapping
			continue
		}

		ent, ok := subscriber.Entitlements[candidateID]
		if !ok {
			// Try case-insensitive lookup
			for k, v := range subscriber.Entitlements {
				if strings.EqualFold(k, candidateID) {
					ent = v
					ok = true
					break
				}
			}
		}

		if !ok {
			continue
		}

		// Parse expiration date
		var candExpiresAt *time.Time
		if ent.ExpiresDate != nil && strings.TrimSpace(*ent.ExpiresDate) != "" {
			if t, err := parseRevenueCatTime(*ent.ExpiresDate); err == nil {
				candExpiresAt = &t
			}
		}

		// Parse purchase date
		var candPurchaseAt *time.Time
		if ent.PurchaseDate != nil && strings.TrimSpace(*ent.PurchaseDate) != "" {
			if t, err := parseRevenueCatTime(*ent.PurchaseDate); err == nil {
				candPurchaseAt = &t
			}
		}

		// Check if entitlement is active (not expired)
		if candExpiresAt != nil && candExpiresAt.Before(now) {
			continue // Expired
		}

		// Found active entitlement
		entitlementID = candidateID
		expiresAt = candExpiresAt
		purchaseAt = candPurchaseAt
		productID = strings.TrimSpace(ent.ProductIdentifier)
		tier = p.MapEntitlementToTier(candidateID)

		return tier, expiresAt, purchaseAt, productID, entitlementID
	}

	// No active entitlement found - return default tier
	return p.defaultTier, nil, nil, "", ""
}

// parseRevenueCatTime parses a RevenueCat timestamp string
func parseRevenueCatTime(value string) (time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}

	// Try RFC3339Nano first (RevenueCat often uses this)
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}

	// Fallback to RFC3339
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unable to parse time: %s", v)
}
