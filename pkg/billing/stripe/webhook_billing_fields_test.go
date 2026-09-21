package stripe

import (
	"testing"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
)

// Stripe keeps price and the current billing period on the subscription item,
// not the subscription, and `currency` is lowercase ISO. The mapping must use
// those fields.
func TestApplyStripeSubscriptionFacts(t *testing.T) {
	start := time.Unix(1700000000, 0).UTC()
	end := time.Unix(1731536000, 0).UTC()

	sub := &stripe.Subscription{
		CancelAtPeriodEnd: true,
		Currency:          "eur",
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					CurrentPeriodStart: start.Unix(),
					CurrentPeriodEnd:   end.Unix(),
					Price: &stripe.Price{
						ID:         "price_123",
						UnitAmount: 4999,
						Currency:   "eur",
					},
				},
			},
		},
	}

	var event billing.WebhookEvent
	applyStripeSubscriptionFacts(&event, sub)

	if event.ProductID != "price_123" {
		t.Errorf("ProductID = %q, want price_123", event.ProductID)
	}
	if event.PriceCents != 4999 {
		t.Errorf("PriceCents = %d, want 4999", event.PriceCents)
	}
	if event.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", event.Currency)
	}
	if event.Store != "stripe" {
		t.Errorf("Store = %q, want stripe", event.Store)
	}
	if event.CancelAtPeriodEnd == nil || !*event.CancelAtPeriodEnd {
		t.Errorf("CancelAtPeriodEnd = %v, want true", event.CancelAtPeriodEnd)
	}
	if event.PurchasedAt == nil || !event.PurchasedAt.Equal(start) {
		t.Errorf("PurchasedAt = %v, want %v", event.PurchasedAt, start)
	}
	if event.ExpiresAt == nil || !event.ExpiresAt.Equal(end) {
		t.Errorf("ExpiresAt = %v, want %v", event.ExpiresAt, end)
	}
}

func TestApplyStripeSubscriptionFacts_NilSafety(t *testing.T) {
	applyStripeSubscriptionFacts(nil, &stripe.Subscription{})
	applyStripeSubscriptionFacts(&billing.WebhookEvent{}, nil)

	event := billing.WebhookEvent{}
	applyStripeSubscriptionFacts(&event, &stripe.Subscription{CancelAtPeriodEnd: false})
	if event.CancelAtPeriodEnd == nil || *event.CancelAtPeriodEnd {
		t.Errorf("CancelAtPeriodEnd = %v, want false", event.CancelAtPeriodEnd)
	}
}
