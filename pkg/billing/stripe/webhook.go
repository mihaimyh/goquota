package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v83"

	"github.com/mihaimyh/goquota/pkg/billing/internal"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// handleWebhook processes incoming Stripe webhook events
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
	body, err := internal.ReadBodyStrict(w, r, 256*1024)
	if err != nil {
		if errors.Is(err, internal.ErrPayloadTooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			p.metrics.RecordWebhookError(providerName, "payload_too_large")
		} else {
			http.Error(w, fmt.Sprintf("invalid payload: %v", err), http.StatusBadRequest)
			p.metrics.RecordWebhookError(providerName, "invalid_payload")
		}
		return
	}

	// Extract signature from header
	sig := r.Header.Get("Stripe-Signature")
	if sig == "" {
		sig = r.Header.Get("stripe-signature")
	}

	// Verify webhook signature (v83 uses stripe.ConstructEvent directly)
	event, err := stripe.ConstructEvent(body, sig, string(p.webhookSecret))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		p.metrics.RecordWebhookError(providerName, "auth_failed")
		return
	}

	eventType := string(event.Type)
	if eventType == "" {
		eventType = "UNKNOWN"
	}

	// Process webhook event
	if err := p.processWebhookEvent(r.Context(), &event); err != nil {
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
func (p *Provider) processWebhookEvent(ctx context.Context, event *stripe.Event) error {
	eventTimestamp := time.Unix(event.Created, 0)

	switch event.Type {
	case "customer.subscription.created":
		return p.handleSubscriptionCreated(ctx, event, eventTimestamp)
	case "customer.subscription.updated":
		return p.handleSubscriptionUpdated(ctx, event, eventTimestamp)
	case "customer.subscription.deleted":
		return p.handleSubscriptionDeleted(ctx, event, eventTimestamp)
	case "invoice.payment_succeeded":
		return p.handleInvoicePaymentSucceeded(ctx, event, eventTimestamp)
	case "invoice.payment_failed":
		return p.handleInvoicePaymentFailed(ctx, event, eventTimestamp)
	case "checkout.session.completed":
		return p.handleCheckoutSessionCompleted(ctx, event, eventTimestamp)
	default:
		// Unknown event type - ignore silently
		return nil
	}
}

// handleSubscriptionCreated processes customer.subscription.created events
func (p *Provider) handleSubscriptionCreated(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to unmarshal subscription: %w", err)
	}

	userID, err := p.extractUserIDFromSubscription(ctx, &subscription)
	if err != nil {
		return fmt.Errorf("failed to extract user_id: %w", err)
	}

	tier, expiresAt, startDate := p.extractTierFromSubscription(&subscription)

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
	tierChanged := previousTier != tier

	// Record tier change metric
	if tierChanged {
		p.metrics.RecordTierChange(providerName, previousTier, tier)
	}

	// Set subscription start date
	var subscriptionStartDate time.Time
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if tier != p.defaultTier && startDate != nil {
		subscriptionStartDate = startOfDayUTC(startDate.UTC())
	} else if tier != p.defaultTier {
		subscriptionStartDate = startOfDayUTC(time.Now().UTC())
	}

	// Create entitlement with event timestamp
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: subscriptionStartDate,
		UpdatedAt:             eventTimestamp,
	}

	if expiresAt != nil {
		ent.ExpiresAt = expiresAt
	}

	return p.manager.SetEntitlement(ctx, ent)
}

// handleSubscriptionUpdated processes customer.subscription.updated events
func (p *Provider) handleSubscriptionUpdated(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to unmarshal subscription: %w", err)
	}

	userID, err := p.extractUserIDFromSubscription(ctx, &subscription)
	if err != nil {
		return fmt.Errorf("failed to extract user_id: %w", err)
	}

	tier, expiresAt, startDate := p.extractTierFromSubscription(&subscription)

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
	tierChanged := previousTier != tier

	// Record tier change metric
	if tierChanged {
		p.metrics.RecordTierChange(providerName, previousTier, tier)
	}

	// Set subscription start date
	var subscriptionStartDate time.Time
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if tier != p.defaultTier && startDate != nil {
		subscriptionStartDate = startOfDayUTC(startDate.UTC())
	} else if tier != p.defaultTier {
		subscriptionStartDate = startOfDayUTC(time.Now().UTC())
	}

	// Create entitlement with event timestamp
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: subscriptionStartDate,
		UpdatedAt:             eventTimestamp,
	}

	if expiresAt != nil {
		ent.ExpiresAt = expiresAt
	}

	return p.manager.SetEntitlement(ctx, ent)
}

// handleSubscriptionDeleted processes customer.subscription.deleted events
func (p *Provider) handleSubscriptionDeleted(ctx context.Context, event *stripe.Event, eventTimestamp time.Time) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("failed to unmarshal subscription: %w", err)
	}

	userID, err := p.extractUserIDFromSubscription(ctx, &subscription)
	if err != nil {
		return fmt.Errorf("failed to extract user_id: %w", err)
	}

	// Get existing entitlement for timestamp comparison
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}

	// Timestamp-based idempotency
	if existing != nil && !eventTimestamp.After(existing.UpdatedAt) {
		return nil
	}

	// Instead of setting default tier directly, re-sync to check for other subscriptions
	// This handles the case where a user has multiple subscriptions and only one is deleted
	_, err = p.SyncUser(ctx, userID)
	return err
}

// handleInvoicePaymentSucceeded processes invoice.payment_succeeded events
//
//nolint:gocyclo // Complex business logic with multiple validation steps
func (p *Provider) handleInvoicePaymentSucceeded(
	ctx context.Context, event *stripe.Event, eventTimestamp time.Time,
) error {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("failed to unmarshal invoice: %w", err)
	}

	// Extract subscription ID from raw JSON (v83 Invoice struct might not have Subscription field directly)
	var rawData map[string]interface{}
	subscriptionID := ""
	if err := json.Unmarshal(event.Data.Raw, &rawData); err == nil {
		switch v := rawData["subscription"].(type) {
		case map[string]interface{}:
			if id, ok := v["id"].(string); ok {
				subscriptionID = id
			}
		case string:
			// Sometimes subscription is just an ID string
			subscriptionID = v
		}
	}
	if subscriptionID == "" {
		// Not a subscription invoice - ignore
		return nil
	}

	// Fetch subscription to get full details (using new client API)
	sub, err := p.stripeClient.V1Subscriptions.Retrieve(ctx, subscriptionID, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch subscription: %w", err)
	}

	userID, err := p.extractUserIDFromSubscription(ctx, sub)
	if err != nil {
		return fmt.Errorf("failed to extract user_id: %w", err)
	}

	// Get existing entitlement
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}

	// Timestamp-based idempotency
	if existing != nil && !eventTimestamp.After(existing.UpdatedAt) {
		return nil
	}

	// Update expiration date to next period
	tier, expiresAt, startDate := p.extractTierFromSubscription(sub)

	var subscriptionStartDate time.Time
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if startDate != nil {
		subscriptionStartDate = startOfDayUTC(startDate.UTC())
	}

	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: subscriptionStartDate,
		UpdatedAt:             eventTimestamp,
	}

	if expiresAt != nil {
		ent.ExpiresAt = expiresAt
	}

	return p.manager.SetEntitlement(ctx, ent)
}

// handleInvoicePaymentFailed processes invoice.payment_failed events
func (p *Provider) handleInvoicePaymentFailed(_ context.Context, event *stripe.Event, _ time.Time) error {
	// Log warning but don't change entitlement
	// The subscription will remain active until it's actually canceled
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("failed to unmarshal invoice: %w", err)
	}

	// Record metric for monitoring
	p.metrics.RecordWebhookEvent(providerName, "invoice.payment_failed", "warning")
	return nil
}

// handleCheckoutSessionCompleted processes checkout.session.completed events
// CRITICAL: This immediately calls SetEntitlement after patching metadata
//
//nolint:gocyclo // Complex business logic with metadata patching and entitlement updates
func (p *Provider) handleCheckoutSessionCompleted(
	ctx context.Context, event *stripe.Event, eventTimestamp time.Time,
) error {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return fmt.Errorf("failed to unmarshal checkout session: %w", err)
	}

	userID := ""
	if session.Metadata != nil {
		userID = session.Metadata["user_id"]
	}
	if userID == "" {
		return fmt.Errorf("metadata.user_id missing on checkout session %s", session.ID)
	}

	subscriptionID := ""
	if session.Subscription != nil {
		subscriptionID = session.Subscription.ID
	}
	if subscriptionID == "" {
		// Not a subscription checkout - ignore
		return nil
	}

	// 1. Patch Stripe subscription metadata (if needed)
	sub, err := p.stripeClient.V1Subscriptions.Retrieve(ctx, subscriptionID, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch subscription: %w", err)
	}

	if sub.Metadata == nil || sub.Metadata["user_id"] == "" {
		// Update subscription metadata on Stripe
		params := &stripe.SubscriptionUpdateParams{}
		params.AddMetadata("user_id", userID)
		sub, err = p.stripeClient.V1Subscriptions.Update(ctx, subscriptionID, params)
		if err != nil {
			return fmt.Errorf("failed to patch subscription metadata: %w", err)
		}
		// Sub is already updated, no need to re-fetch
	}

	// 2. IMMEDIATELY update GoQuota entitlement (don't wait for next webhook)
	tier, expiresAt, startDate := p.extractTierFromSubscription(sub)

	// Get existing entitlement for timestamp comparison
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}

	// Timestamp-based idempotency
	if existing != nil && !eventTimestamp.After(existing.UpdatedAt) {
		return nil
	}

	var subscriptionStartDate time.Time
	if existing != nil && !existing.SubscriptionStartDate.IsZero() {
		subscriptionStartDate = existing.SubscriptionStartDate
	} else if startDate != nil {
		subscriptionStartDate = startOfDayUTC(startDate.UTC())
	} else {
		subscriptionStartDate = startOfDayUTC(time.Now().UTC())
	}

	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: subscriptionStartDate,
		ExpiresAt:             expiresAt,
		UpdatedAt:             eventTimestamp,
	}

	return p.manager.SetEntitlement(ctx, ent)
}

// extractUserIDFromSubscription extracts user_id from subscription or customer metadata
func (p *Provider) extractUserIDFromSubscription(ctx context.Context, sub *stripe.Subscription) (string, error) {
	// 1. Check subscription metadata
	if sub.Metadata != nil {
		if userID, ok := sub.Metadata["user_id"]; ok && userID != "" {
			return userID, nil
		}
	}

	// 2. Fallback to customer metadata
	if sub.Customer != nil {
		cust, err := p.stripeClient.V1Customers.Retrieve(ctx, sub.Customer.ID, nil)
		if err == nil && cust.Metadata != nil {
			if userID, ok := cust.Metadata["user_id"]; ok && userID != "" {
				return userID, nil
			}
		}
	}

	// 3. Not found - log structured warning
	return "", fmt.Errorf("metadata.user_id missing on subscription %s", sub.ID)
}

// extractTierFromSubscription extracts tier information from a Stripe subscription
// Returns tier, expiresAt, and startDate. Period dates are always nil as they come from webhook event JSON.
//
//nolint:unparam // expiresAt and startDate are part of the API contract, even though they're always nil
func (p *Provider) extractTierFromSubscription(
	sub *stripe.Subscription,
) (tier string, expiresAt, startDate *time.Time) {
	var highestTier string
	var maxWeight = -1
	var mostRecentCreated int64

	if sub.Status != subscriptionStatusActive {
		return p.defaultTier, nil, nil
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
			// Period dates (expiresAt, startDate) are set via webhook events
			// which provide the current_period_start/end in the event payload
		}
	}

	if highestTier == "" {
		return p.defaultTier, nil, nil
	}

	return highestTier, expiresAt, startDate
}

// Helper functions

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func startOfDayUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
