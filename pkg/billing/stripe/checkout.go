package stripe

import (
	"context"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
)

// CheckoutURL creates a Stripe Checkout Session and returns the URL.
// The tier is automatically resolved to a Stripe Price ID using the configured TierMapping.
func (p *Provider) CheckoutURL(ctx context.Context, userID, tier, successURL, cancelURL string) (string, error) {
	startTime := time.Now()

	// 1. Resolve tier to Stripe Price ID
	priceID := p.getPriceIDForTier(tier)
	if priceID == "" {
		p.metrics.RecordAPICall(providerName, "/checkout/sessions", "tier_not_found")
		return "", fmt.Errorf("%w: %s", billing.ErrTierNotConfigured, tier)
	}

	// 2. Resolve Customer ID (optional - Stripe can create customer during checkout)
	// Error intentionally ignored - Stripe creates customer if not found
	customerID, _ := p.resolveCustomerID(ctx, userID) //nolint:errcheck

	// 3. Create Checkout Session
	params := &stripe.CheckoutSessionCreateParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
	}

	// CRITICAL: Inject metadata for webhook handler
	params.SubscriptionData = &stripe.CheckoutSessionCreateSubscriptionDataParams{}
	params.SubscriptionData.AddMetadata("user_id", userID)

	// Attach existing customer if found (avoids duplicates)
	if customerID != "" {
		params.Customer = stripe.String(customerID)
	} else {
		// Use ClientReferenceID to link new customer to userID
		params.ClientReferenceID = stripe.String(userID)

		// Also set customer creation and update settings
		params.CustomerCreation = stripe.String("always")
	}

	// Create session using the correct SDK v84 API
	session, err := p.stripeClient.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		p.metrics.RecordAPICall(providerName, "/checkout/sessions", "error")
		p.metrics.RecordAPICallDuration(providerName, "/checkout/sessions", time.Since(startTime))
		return "", fmt.Errorf("failed to create checkout session: %w", err)
	}

	p.metrics.RecordAPICall(providerName, "/checkout/sessions", "success")
	p.metrics.RecordAPICallDuration(providerName, "/checkout/sessions", time.Since(startTime))

	return session.URL, nil
}

// PortalURL creates a Stripe Customer Portal Session and returns the URL.
// This allows users to manage their subscription, update payment methods, or cancel.
func (p *Provider) PortalURL(ctx context.Context, userID, returnURL string) (string, error) {
	startTime := time.Now()

	// 1. Resolve Customer ID (required for portal)
	customerID, err := p.resolveCustomerID(ctx, userID)
	if err != nil {
		p.metrics.RecordAPICall(providerName, "/billing_portal/sessions", "customer_not_found")
		return "", fmt.Errorf("%w: %s", billing.ErrCustomerNotFound, userID)
	}

	// 2. Create Portal Session
	params := &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}

	session, err := p.stripeClient.V1BillingPortalSessions.Create(ctx, params)
	if err != nil {
		p.metrics.RecordAPICall(providerName, "/billing_portal/sessions", "error")
		p.metrics.RecordAPICallDuration(providerName, "/billing_portal/sessions", time.Since(startTime))
		return "", fmt.Errorf("failed to create portal session: %w", err)
	}

	p.metrics.RecordAPICall(providerName, "/billing_portal/sessions", "success")
	p.metrics.RecordAPICallDuration(providerName, "/billing_portal/sessions", time.Since(startTime))

	return session.URL, nil
}

// getPriceIDForTier returns the Stripe Price ID for a given tier name.
// This is the reverse of MapPriceToTier.
//
// Note: If multiple Price IDs map to the same tier (e.g., monthly and yearly),
// this returns the first match found. For production use with multiple billing
// cycles, consider mapping them as distinct tiers in your configuration
// (e.g., "pro_monthly", "pro_yearly") or add a billing cycle parameter.
func (p *Provider) getPriceIDForTier(tier string) string {
	for priceID, mappedTier := range p.tierMapping {
		if mappedTier == tier {
			return priceID
		}
	}
	return ""
}

// resolveCustomerID attempts to find the Stripe Customer ID for a user.
// Uses the fast path (CustomerIDResolver) if available, otherwise falls back
// to the slow Stripe Search API.
func (p *Provider) resolveCustomerID(ctx context.Context, userID string) (string, error) {
	// FAST PATH: App provides the mapping (O(1))
	if p.customerIDResolver != nil {
		customerID, err := p.customerIDResolver(ctx, userID)
		if err == nil && customerID != "" {
			return customerID, nil
		}
	}

	// SLOW PATH: Stripe Search API (O(N), ~500ms)
	return p.searchCustomerByMetadata(ctx, userID)
}
