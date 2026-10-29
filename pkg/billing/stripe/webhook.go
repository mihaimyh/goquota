package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/billing/internal"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Stripe event type constants
const (
	eventTypeSubscriptionCreated = "customer.subscription.created"
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
	case eventTypeSubscriptionCreated:
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
	case "payment_intent.refunded":
		return p.handlePaymentIntentRefunded(ctx, event, eventTimestamp)
	case "charge.refunded":
		return p.handleChargeRefunded(ctx, event, eventTimestamp)
	default:
		// Unknown event type - ignore silently
		return nil
	}
}

// handleSubscriptionCreated processes customer.subscription.created events
//
//nolint:gocyclo // Complex business logic for subscription creation
func (p *Provider) handleSubscriptionCreated(
	ctx context.Context,
	event *stripe.Event,
	eventTimestamp time.Time,
) error {
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

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	// Invoke webhook callback if configured
	metadata := make(map[string]interface{})
	if subscription.Metadata != nil {
		metadata["subscription_metadata"] = subscription.Metadata
	}
	ev := billing.WebhookEvent{
		UserID:         userID,
		PreviousTier:   previousTier,
		NewTier:        tier,
		Provider:       providerName,
		EventType:      string(event.Type),
		EventTimestamp: eventTimestamp,
		ExpiresAt:      expiresAt,
		EventID:        event.ID,
		Metadata:       metadata,
	}
	applyStripeSubscriptionFacts(&ev, &subscription)
	return p.invokeWebhookCallback(ctx, ev)
}

// handleSubscriptionUpdated processes customer.subscription.updated events
//
//nolint:gocyclo // Complex business logic for subscription updates
func (p *Provider) handleSubscriptionUpdated(
	ctx context.Context,
	event *stripe.Event,
	eventTimestamp time.Time,
) error {
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

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	// Invoke webhook callback if configured
	metadata := make(map[string]interface{})
	if subscription.Metadata != nil {
		metadata["subscription_metadata"] = subscription.Metadata
	}
	ev := billing.WebhookEvent{
		UserID:         userID,
		PreviousTier:   previousTier,
		NewTier:        tier,
		Provider:       providerName,
		EventType:      string(event.Type),
		EventTimestamp: eventTimestamp,
		ExpiresAt:      expiresAt,
		EventID:        event.ID,
		Metadata:       metadata,
	}
	applyStripeSubscriptionFacts(&ev, &subscription)
	return p.invokeWebhookCallback(ctx, ev)
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

	// API key is required for invoice payment processing
	if p.stripeClient == nil {
		return fmt.Errorf("STRIPE_API_KEY required for invoice payment processing")
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

	// Determine previous tier for callback
	previousTier := p.defaultTier
	if existing != nil {
		previousTier = existing.Tier
	}

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	// Invoke webhook callback if configured
	metadata := make(map[string]interface{})
	if sub.Metadata != nil {
		metadata["subscription_metadata"] = sub.Metadata
	}
	ev := billing.WebhookEvent{
		UserID:         userID,
		PreviousTier:   previousTier,
		NewTier:        tier,
		Provider:       providerName,
		EventType:      string(event.Type),
		EventTimestamp: eventTimestamp,
		ExpiresAt:      expiresAt,
		EventID:        event.ID,
		Metadata:       metadata,
	}
	applyStripeSubscriptionFacts(&ev, sub)
	return p.invokeWebhookCallback(ctx, ev)
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

// handleCheckoutSessionCompleted processes checkout.session.completed events.
// Only paid one-time (payment mode) sessions that carry credit-pack metadata
// move quota; subscription purchases are owned by the subscription events.
func (p *Provider) handleCheckoutSessionCompleted(
	ctx context.Context, event *stripe.Event, eventTimestamp time.Time,
) error {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return fmt.Errorf("failed to unmarshal checkout session: %w", err)
	}

	// customer.subscription.* and invoice.payment_succeeded own the entitlement
	// for subscriptions; granting here as well would apply the same purchase
	// twice when Stripe delivers this event after the subscription events.
	if session.Mode == stripe.CheckoutSessionModeSubscription {
		return nil
	}

	userID := ""
	if session.Metadata != nil {
		userID = session.Metadata["user_id"]
	}
	if userID == "" {
		return fmt.Errorf("metadata.user_id missing on checkout session %s", session.ID)
	}

	// Checkout completes before the money lands for delayed payment methods
	// (SEPA, Boleto). Granting credits here would hand out the pack for a
	// payment that may still fail.
	if session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		p.metrics.RecordWebhookEvent(providerName, string(event.Type), "ignored")
		return nil
	}

	// Only credit-pack sessions carry the resource metadata. Any other
	// one-time payment is not ours to process, so ignore it gracefully.
	if session.Metadata["resource"] == "" {
		return nil
	}

	if err := p.handlePaymentCheckout(ctx, &session, eventTimestamp); err != nil {
		return err
	}

	tier := "unknown"
	if session.Metadata != nil {
		// Try to get tier from metadata if available
		if t, ok := session.Metadata["tier"]; ok {
			tier = t
		}
	}
	p.metrics.RecordCheckoutSessionCompleted(providerName, tier, "payment")

	// Record revenue estimate (amount in cents, convert to dollars)
	if session.AmountTotal > 0 {
		revenueAmount := float64(session.AmountTotal) / 100.0
		p.metrics.RecordRevenueEstimate(providerName, tier, "one-time", revenueAmount)
	}

	paymentIntentID := ""
	if session.PaymentIntent != nil {
		paymentIntentID = session.PaymentIntent.ID
	}

	// The quota already moved; report the purchase so the consumer can mirror it
	// in its own ledger.
	return p.invokeWebhookCallback(ctx, billing.WebhookEvent{
		UserID:           userID,
		Provider:         providerName,
		EventType:        string(event.Type),
		EventID:          event.ID,
		ProviderObjectID: session.ID,
		PaymentIntentID:  paymentIntentID,
		EventTimestamp:   eventTimestamp,
		Store:            providerName,
		Currency:         strings.ToUpper(string(session.Currency)),
		PriceCents:       session.AmountTotal,
		Metadata: map[string]interface{}{
			"resource":          session.Metadata["resource"],
			"credits":           int(session.AmountTotal),
			"payment_intent_id": paymentIntentID,
		},
	})
}

// extractUserIDFromSubscription extracts user_id from subscription or customer metadata
func (p *Provider) extractUserIDFromSubscription(ctx context.Context, sub *stripe.Subscription) (string, error) {
	// 1. Check subscription metadata
	if sub.Metadata != nil {
		if userID, ok := sub.Metadata["user_id"]; ok && userID != "" {
			return userID, nil
		}
	}

	// 2. Fallback to customer metadata (requires API key)
	if sub.Customer != nil && p.stripeClient != nil {
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
// Returns tier, expiresAt, and startDate.
// In Stripe SDK v84+, CurrentPeriodEnd and CurrentPeriodStart are on SubscriptionItem.
func (p *Provider) extractTierFromSubscription(
	sub *stripe.Subscription,
) (tier string, expiresAt, startDate *time.Time) {
	var highestTier string
	var maxWeight = -1
	var mostRecentCreated int64
	var periodEnd *time.Time
	var periodStart *time.Time

	if !isSubscriptionStatusValidForAccess(string(sub.Status)) {
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

	if highestTier == "" {
		return p.defaultTier, nil, nil
	}

	return highestTier, periodEnd, periodStart
}

// Helper functions

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// handlePaymentCheckout processes one-time payment checkout sessions
func (p *Provider) handlePaymentCheckout(
	ctx context.Context, session *stripe.CheckoutSession, _ time.Time,
) error {
	userID := ""
	resource := ""
	if session.Metadata != nil {
		userID = session.Metadata["user_id"]
		resource = session.Metadata["resource"]
	}
	if userID == "" {
		// Fallback to ClientReferenceID
		userID = session.ClientReferenceID
	}
	if userID == "" {
		return fmt.Errorf("metadata.user_id or client_reference_id missing on checkout session %s", session.ID)
	}
	if resource == "" {
		return fmt.Errorf("metadata.resource missing on checkout session %s", session.ID)
	}

	// Extract amount from line items
	var amount int
	if len(session.LineItems.Data) > 0 {
		// Amount is in cents, convert to credits (1 cent = 1 credit, or use conversion factor)
		// For now, assume 1 cent = 1 credit (can be made configurable)
		amount = int(session.AmountTotal)
	} else {
		return fmt.Errorf("no line items found in checkout session %s", session.ID)
	}

	// Get payment intent ID for idempotency
	paymentIntentID := ""
	if session.PaymentIntent != nil {
		paymentIntentID = session.PaymentIntent.ID
	}
	if paymentIntentID == "" {
		return fmt.Errorf("payment_intent missing on checkout session %s", session.ID)
	}

	// Call TopUpLimit with idempotency key
	err := p.manager.TopUpLimit(ctx, userID, resource, amount, goquota.WithTopUpIdempotencyKey(paymentIntentID))
	if err != nil {
		return fmt.Errorf("failed to top up limit: %w", err)
	}

	// Record credit pack purchase (amount is in cents, convert to credits)
	// For metrics, use amount range
	amountRange := getAmountRangeFromCents(session.AmountTotal)
	p.metrics.RecordCreditPackPurchase(providerName, resource, amountRange)

	return nil
}

// getAmountRangeFromCents converts an amount in cents to a range string for metrics
func getAmountRangeFromCents(amountCents int64) string {
	amount := float64(amountCents) / 100.0 // Convert cents to dollars
	switch {
	case amount < 10:
		return amountRange0_10
	case amount < 50:
		return amountRange10_50
	case amount < 100:
		return amountRange50_100
	case amount < 500:
		return amountRange100_500
	case amount < 1000:
		return amountRange500_1000
	default:
		return amountRange1000Plus
	}
}

// handleChargeRefunded processes charge.refunded events.
//
// `amount_refunded` on a charge is cumulative, so converting it into credits
// revokes 20, then 40, then 60 credits for three 20% refunds. Only the
// individual refund entry describes what this event took back; a partial refund
// without one is acknowledged instead of over-revoking.
func (p *Provider) handleChargeRefunded(ctx context.Context, event *stripe.Event, _ time.Time) error {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		return fmt.Errorf("failed to unmarshal charge: %w", err)
	}

	paymentIntentID := ""
	if charge.PaymentIntent != nil {
		paymentIntentID = charge.PaymentIntent.ID
	}

	metadata := p.chargeMetadata(ctx, &charge)
	userID := metadata["user_id"]
	resource := metadata["resource"]
	if userID == "" || resource == "" {
		// Refunds of subscription invoices reach this event without credit-pack
		// metadata. They move no credits, but must still be acknowledged.
		return p.acknowledgeNonCreditRefund(
			ctx, event, charge.ID, paymentIntentID, charge.AmountRefunded, string(charge.Currency),
		)
	}

	credits, identity, refundedAmount, ok := chargeRefundDecision(&charge, metadata, paymentIntentID, event.ID)
	if !ok {
		p.metrics.RecordWebhookEvent(providerName, string(event.Type), "ignored")
		return nil
	}

	return p.applyCreditRefund(ctx, event, userID, resource, credits, refundedAmount,
		identity, charge.ID, paymentIntentID, string(charge.Currency))
}

// chargeMetadata returns the metadata that describes the purchase behind a
// charge. Stripe does not copy PaymentIntent metadata onto the Charge it
// settles, so a credit-pack refund can arrive with an empty Charge.Metadata;
// fall back to the payment intent, first from the payload and then from the API.
func (p *Provider) chargeMetadata(ctx context.Context, charge *stripe.Charge) map[string]string {
	if charge == nil {
		return nil
	}
	if charge.Metadata["user_id"] != "" && charge.Metadata["resource"] != "" {
		return charge.Metadata
	}

	paymentIntent := charge.PaymentIntent
	if paymentIntent == nil {
		return charge.Metadata
	}
	if paymentIntent.Metadata["user_id"] != "" && paymentIntent.Metadata["resource"] != "" {
		return paymentIntent.Metadata
	}
	if p.stripeClient == nil {
		return charge.Metadata
	}

	fetched, err := p.stripeClient.V1PaymentIntents.Retrieve(ctx, paymentIntent.ID, nil)
	if err != nil || fetched == nil {
		return charge.Metadata
	}
	return fetched.Metadata
}

// chargeRefundDecision works out how many credits this charge.refunded event
// takes back and under which idempotency identity. ok is false when the event
// cannot be turned into a credit movement and must be acknowledged untouched.
func chargeRefundDecision(
	charge *stripe.Charge, metadata map[string]string, paymentIntentID, eventID string,
) (credits int, identity string, refundedAmount int64, ok bool) {
	if charge == nil || charge.Amount <= 0 {
		return 0, "", 0, false
	}
	packCredits := packCreditsFromMetadata(metadata, charge.Amount)

	// Stripe lists refunds newest first, so the newest entry is the one this
	// event reports. Guard for an unexpanded or empty list.
	if charge.Refunds != nil && len(charge.Refunds.Data) > 0 {
		refund := charge.Refunds.Data[0]
		if refund == nil || refund.Amount <= 0 {
			return 0, "", 0, false
		}

		refundedAmount = refund.Amount
		credits = proratedCredits(packCredits, refund.Amount, charge.Amount)
		if credits <= 0 {
			return 0, "", refundedAmount, false
		}
		if isFullChargeRefund(charge) {
			return credits, refundFullIdentity(paymentIntentID, charge.ID), refundedAmount, true
		}

		identity = refund.ID
		if identity == "" {
			identity = eventID
		}
		return credits, identity, refundedAmount, true
	}

	// Without a refund entry only a full refund can be established.
	refundedAmount = charge.AmountRefunded
	if refundedAmount <= 0 || !isFullChargeRefund(charge) {
		return 0, "", refundedAmount, false
	}

	credits = proratedCredits(packCredits, refundedAmount, charge.Amount)
	if credits <= 0 {
		return 0, "", refundedAmount, false
	}
	return credits, refundFullIdentity(paymentIntentID, charge.ID), refundedAmount, true
}

// handlePaymentIntentRefunded processes payment_intent.refunded events.
func (p *Provider) handlePaymentIntentRefunded(
	ctx context.Context, event *stripe.Event, _ time.Time,
) error {
	// stripe.PaymentIntent has no AmountRefunded field, but the webhook payload
	// carries it; read it from the raw JSON alongside the SDK struct.
	var paymentIntent stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
		return fmt.Errorf("failed to unmarshal payment intent: %w", err)
	}
	var refundPayload struct {
		AmountRefunded int64 `json:"amount_refunded"`
	}
	if err := json.Unmarshal(event.Data.Raw, &refundPayload); err != nil {
		return fmt.Errorf("failed to unmarshal payment intent refund: %w", err)
	}
	amountRefunded := refundPayload.AmountRefunded

	userID := paymentIntent.Metadata["user_id"]
	resource := paymentIntent.Metadata["resource"]
	if userID == "" || resource == "" {
		// The event carries no credit-pack metadata, so there is nothing to
		// revoke. Report it and ack instead of failing the delivery.
		return p.acknowledgeNonCreditRefund(
			ctx, event, paymentIntent.ID, paymentIntent.ID,
			amountRefunded, string(paymentIntent.Currency),
		)
	}

	// This event carries no refund entry, so only a full refund can be
	// established; charge.refunded owns partial refunds.
	if paymentIntent.Amount <= 0 || amountRefunded != paymentIntent.Amount {
		p.metrics.RecordWebhookEvent(providerName, string(event.Type), "ignored")
		return nil
	}

	credits := proratedCredits(
		packCreditsFromMetadata(paymentIntent.Metadata, paymentIntent.Amount),
		amountRefunded,
		paymentIntent.Amount,
	)
	if credits <= 0 {
		p.metrics.RecordWebhookEvent(providerName, string(event.Type), "ignored")
		return nil
	}

	// The payment-intent identity matches the one charge.refunded uses for the
	// same full refund, so the two events revoke the pack once.
	return p.applyCreditRefund(ctx, event, userID, resource, credits, amountRefunded,
		refundFullIdentity(paymentIntent.ID, paymentIntent.ID), paymentIntent.ID, paymentIntent.ID,
		string(paymentIntent.Currency))
}

// applyCreditRefund revokes credits and reports the movement to the webhook
// callback.
func (p *Provider) applyCreditRefund(
	ctx context.Context, event *stripe.Event, userID, resource string,
	credits int, refundedAmount int64, idempotencyKey, providerObjectID, paymentIntentID, currency string,
) error {
	err := p.manager.RefundCredits(
		ctx, userID, resource, credits, "stripe_refund", goquota.WithRefundIdempotencyKey(idempotencyKey),
	)
	if err != nil {
		return fmt.Errorf("failed to refund credits: %w", err)
	}

	return p.invokeWebhookCallback(ctx, billing.WebhookEvent{
		UserID:           userID,
		Provider:         providerName,
		EventType:        string(event.Type),
		EventID:          event.ID,
		ProviderObjectID: providerObjectID,
		PaymentIntentID:  paymentIntentID,
		EventTimestamp:   time.Unix(event.Created, 0),
		Store:            providerName,
		Currency:         strings.ToUpper(currency),
		PriceCents:       refundedAmount,
		Metadata: map[string]interface{}{
			"resource":          resource,
			"credits":           credits,
			"payment_intent_id": paymentIntentID,
		},
	})
}

// acknowledgeNonCreditRefund reports a refund that carries no credit-pack
// metadata (subscription invoice refunds, unrelated charges) and acknowledges
// it. Returning an error here became an HTTP 500, which made Stripe retry the
// same refund forever.
func (p *Provider) acknowledgeNonCreditRefund(
	ctx context.Context, event *stripe.Event, providerObjectID, paymentIntentID string,
	refundedAmount int64, currency string,
) error {
	p.metrics.RecordWebhookEvent(providerName, string(event.Type), "ignored")

	return p.invokeWebhookCallback(ctx, billing.WebhookEvent{
		Provider:         providerName,
		EventType:        string(event.Type),
		EventID:          event.ID,
		ProviderObjectID: providerObjectID,
		PaymentIntentID:  paymentIntentID,
		EventTimestamp:   time.Unix(event.Created, 0),
		Store:            providerName,
		Currency:         strings.ToUpper(currency),
		PriceCents:       refundedAmount,
	})
}

// packCreditsFromMetadata returns how many credits the purchase granted: the
// explicit `credits` metadata when present, otherwise the 1 cent = 1 credit
// rule (the pack amount in minor units).
func packCreditsFromMetadata(metadata map[string]string, originalAmount int64) int {
	if raw := strings.TrimSpace(metadata["credits"]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	if originalAmount <= 0 {
		return 0
	}
	return int(originalAmount)
}

// proratedCredits converts a refunded share of the original amount into the
// matching share of the granted credits.
func proratedCredits(packCredits int, refundedAmount, originalAmount int64) int {
	if packCredits <= 0 || refundedAmount <= 0 || originalAmount <= 0 {
		return 0
	}

	credits := int(math.Round(float64(packCredits) * float64(refundedAmount) / float64(originalAmount)))
	if credits > packCredits {
		credits = packCredits
	}
	if credits < 0 {
		credits = 0
	}
	return credits
}

// isFullChargeRefund reports that the cumulative refunded amount covers the
// whole charge.
func isFullChargeRefund(charge *stripe.Charge) bool {
	return charge.Amount > 0 && charge.AmountRefunded == charge.Amount
}

// refundFullIdentity is the idempotency identity of a full refund. charge.refunded
// and payment_intent.refunded for the same refund resolve to the same key, so the
// pack is revoked once.
func refundFullIdentity(paymentIntentID, fallbackID string) string {
	if paymentIntentID != "" {
		return "refund-full:" + paymentIntentID
	}
	return "refund-full:" + fallbackID
}

func startOfDayUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// invokeWebhookCallback calls the configured webhook callback if present
// invokeWebhookCallback calls the configured webhook callback if present.
func (p *Provider) invokeWebhookCallback(ctx context.Context, event billing.WebhookEvent) error {
	if p.webhookCallback == nil {
		return nil
	}
	if event.Metadata == nil {
		event.Metadata = make(map[string]interface{})
	}
	if event.Provider == "" {
		event.Provider = providerName
	}
	return p.webhookCallback(ctx, event)
}

// applyStripeSubscriptionFacts copies the billing facts a Stripe subscription
// already carries onto the webhook event. Price and the current billing period
// live on the subscription *item* (and its price), not the subscription itself.
func applyStripeSubscriptionFacts(event *billing.WebhookEvent, sub *stripe.Subscription) {
	if event == nil || sub == nil {
		return
	}
	cancelAtPeriodEnd := sub.CancelAtPeriodEnd
	event.CancelAtPeriodEnd = &cancelAtPeriodEnd
	event.Store = "stripe"
	event.Currency = strings.ToUpper(string(sub.Currency))
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return
	}
	item := sub.Items.Data[0]
	if item.Price != nil {
		event.ProductID = item.Price.ID
		if item.Price.UnitAmount > 0 {
			event.PriceCents = item.Price.UnitAmount
		}
		if event.Currency == "" {
			event.Currency = strings.ToUpper(string(item.Price.Currency))
		}
	}
	if item.CurrentPeriodStart > 0 {
		started := time.Unix(item.CurrentPeriodStart, 0).UTC()
		event.PurchasedAt = &started
	}
	if event.ExpiresAt == nil && item.CurrentPeriodEnd > 0 {
		ends := time.Unix(item.CurrentPeriodEnd, 0).UTC()
		event.ExpiresAt = &ends
	}
}
