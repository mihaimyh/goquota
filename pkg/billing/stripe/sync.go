package stripe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// syncUserFromAPI synchronizes a user's entitlement from Stripe API
func (p *Provider) syncUserFromAPI(ctx context.Context, userID string) (string, error) {
	startTime := time.Now()
	if strings.TrimSpace(p.apiKey) == "" {
		p.metrics.RecordUserSync(providerName, "error")
		return p.defaultTier, fmt.Errorf("stripe API key not configured")
	}

	var customerID string
	var err error

	// FAST PATH: App provides the mapping (O(1))
	if p.customerIDResolver != nil {
		customerID, err = p.customerIDResolver(ctx, userID)
		if err == nil && customerID != "" {
			return p.syncCustomer(ctx, customerID, userID, startTime)
		}
		// Log: "CustomerIDResolver returned error, falling back to Search API"
	}

	// SLOW PATH: Stripe Search API (O(N), ~500ms, eventually consistent)
	if customerID == "" {
		p.metrics.RecordAPICall(providerName, "/customers/search", "slow_path")
		customerID, err = p.searchCustomerByMetadata(ctx, userID)
		if err != nil {
			// Customer not found - set to default tier
			return p.syncToDefaultTier(ctx, userID, startTime)
		}
	}

	return p.syncCustomer(ctx, customerID, userID, startTime)
}

// searchCustomerByMetadata searches for a customer by metadata using Stripe Search API
func (p *Provider) searchCustomerByMetadata(ctx context.Context, userID string) (string, error) {
	params := &stripe.CustomerSearchParams{}
	params.Query = fmt.Sprintf("metadata['user_id']:'%s'", userID)

	// Use new client API for Search (v83)
	for cust, err := range p.stripeClient.V1Customers.Search(ctx, params) {
		if err != nil {
			return "", fmt.Errorf("stripe search error: %w", err)
		}
		// Verify exact match (Search API can return partial matches)
		if cust.Metadata != nil && cust.Metadata["user_id"] == userID {
			return cust.ID, nil
		}
	}

	return "", billing.ErrUserNotFound
}

// syncCustomer synchronizes a customer's subscriptions and updates entitlement
func (p *Provider) syncCustomer(ctx context.Context, customerID, userID string, startTime time.Time) (string, error) {
	// Fetch subscriptions for this customer (active, trialing, or past_due)
	// Note: We fetch all subscriptions and filter by valid statuses
	// Stripe API doesn't support filtering by multiple statuses in a single query
	params := &stripe.SubscriptionListParams{}
	params.Customer = stripe.String(customerID)
	// Don't filter by status - we'll filter in code to support multiple valid statuses

	var subscriptions []*stripe.Subscription

	// Use new client API for List (v83)
	for sub, err := range p.stripeClient.V1Subscriptions.List(ctx, params) {
		if err != nil {
			p.metrics.RecordAPICall(providerName, "/subscriptions/list", "error")
			p.metrics.RecordUserSync(providerName, "error")
			p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
			return p.defaultTier, fmt.Errorf("failed to list subscriptions: %w", err)
		}
		// Include subscriptions with valid statuses (active, trialing, past_due)
		if isSubscriptionStatusValidForAccess(string(sub.Status)) {
			subscriptions = append(subscriptions, sub)
		}
	}

	p.metrics.RecordAPICall(providerName, "/subscriptions/list", "200")
	p.metrics.RecordAPICallDuration(providerName, "/subscriptions/list", time.Since(startTime))

	// Resolve tier from subscriptions using weights
	// Note: v83 might not have CurrentPeriodEnd/Start in struct, so we'll calculate from other fields
	tier, expiresAt, startDate := p.resolveTierFromSubscriptions(subscriptions)

	// Get existing entitlement to check for tier changes
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		p.metrics.RecordUserSync(providerName, "error")
		p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
		return tier, err
	}

	previousTier := p.defaultTier
	tierChanged := false
	if existing != nil {
		previousTier = existing.Tier
		tierChanged = previousTier != tier
	}

	// Record tier change metric
	if tierChanged {
		p.metrics.RecordTierChange(providerName, previousTier, tier)
	}

	// Set subscription start date
	subscriptionStartDate := time.Time{}
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if tier != p.defaultTier && startDate != nil {
		subscriptionStartDate = startOfDayUTC(startDate.UTC())
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
		p.metrics.RecordUserSync(providerName, "error")
		p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
		return tier, fmt.Errorf("failed to set entitlement: %w", err)
	}

	p.metrics.RecordUserSync(providerName, "success")
	p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
	return tier, nil
}

// syncToDefaultTier sets a user to the default tier (when not found in Stripe)
func (p *Provider) syncToDefaultTier(ctx context.Context, userID string, startTime time.Time) (string, error) {
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  p.defaultTier,
		SubscriptionStartDate: time.Time{},
		UpdatedAt:             time.Now().UTC(),
	}

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		p.metrics.RecordUserSync(providerName, "error")
		p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
		return p.defaultTier, fmt.Errorf("failed to set default tier: %w", err)
	}

	p.metrics.RecordUserSync(providerName, "success")
	p.metrics.RecordUserSyncDuration(providerName, time.Since(startTime))
	return p.defaultTier, nil
}

// resolveTierFromSubscriptions resolves tier from multiple subscriptions using tier weights
func (p *Provider) resolveTierFromSubscriptions(
	subscriptions []*stripe.Subscription,
) (tier string, expiresAt, startDate *time.Time) {
	var highestTier string
	var maxWeight = -1
	var mostRecentCreated int64
	var periodEnd *time.Time
	var periodStart *time.Time

	for _, sub := range subscriptions {
		// Only process subscriptions with valid statuses (already filtered, but double-check)
		if !isSubscriptionStatusValidForAccess(string(sub.Status)) {
			continue
		}

		// Extract tier from subscription items
		for _, item := range sub.Items.Data {
			priceID := item.Price.ID
			tier := p.MapPriceToTier(priceID)
			weight := p.GetTierWeight(tier)

			// Select highest weight tier
			// If tie, use most recently created subscription
			if weight > maxWeight || (weight == maxWeight && sub.Created > mostRecentCreated) {
				maxWeight = weight
				highestTier = tier
				mostRecentCreated = sub.Created

				// In Stripe SDK v84+, CurrentPeriodEnd and CurrentPeriodStart are on SubscriptionItem
				// (they were moved from Subscription to SubscriptionItem in API version 2025-03-31.basil)
				if item.CurrentPeriodEnd > 0 {
					t := time.Unix(item.CurrentPeriodEnd, 0).UTC()
					periodEnd = &t
				}
				if item.CurrentPeriodStart > 0 {
					t := time.Unix(item.CurrentPeriodStart, 0).UTC()
					periodStart = &t
				}
			}
		}
	}

	if highestTier == "" {
		return p.defaultTier, nil, nil
	}

	return highestTier, periodEnd, periodStart
}
