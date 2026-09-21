package revenuecat

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
)

func captureBillingEvent(t *testing.T, payload webhookPayload) billing.WebhookEvent {
	t.Helper()
	manager := mockManager(t)
	ctx := context.Background()

	var captured billing.WebhookEvent
	provider, err := NewProvider(billing.Config{
		Manager:       manager,
		TierMapping:   map[string]string{"premium": "pro"},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
			captured = event
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if err := provider.processWebhookEvent(ctx, &payload, payload.Event.AppUserID); err != nil {
		t.Fatalf("processWebhookEvent: %v", err)
	}
	return captured
}

// The callback must carry the billing facts providers already send, so
// consumers do not have to re-parse the raw webhook body.
func TestWebhookCallback_BillingFields(t *testing.T) {
	eventTime := time.Now()
	purchasedAt := eventTime.Add(-time.Hour)
	expiresAt := eventTime.AddDate(1, 0, 0)

	payload := createTestPayload("test_user_123", "premium", eventTime)
	payload.Event.ID = "evt_123"
	payload.Event.Store = "PLAY_STORE"
	payload.Event.Currency = "eur"
	payload.Event.PriceInPurchasedCurrency = 49.99
	payload.Event.PeriodType = "NORMAL"
	payload.Event.PurchasedAtMs = purchasedAt.UnixMilli()
	payload.Event.ExpirationAtMs = expiresAt.UnixMilli()

	event := captureBillingEvent(t, payload)

	if event.EventID != "evt_123" {
		t.Errorf("EventID = %q, want evt_123", event.EventID)
	}
	if event.ProductID != "premium" {
		t.Errorf("ProductID = %q, want premium", event.ProductID)
	}
	if event.Store != "PLAY_STORE" {
		t.Errorf("Store = %q, want PLAY_STORE", event.Store)
	}
	if event.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", event.Currency)
	}
	if event.PriceCents != 4999 {
		t.Errorf("PriceCents = %d, want 4999", event.PriceCents)
	}
	if event.PeriodType != "NORMAL" {
		t.Errorf("PeriodType = %q, want NORMAL", event.PeriodType)
	}
	if event.PurchasedAt == nil || !event.PurchasedAt.Equal(purchasedAt.UTC().Truncate(time.Millisecond)) {
		t.Errorf("PurchasedAt = %v, want %v", event.PurchasedAt, purchasedAt.UTC())
	}
	if event.ExpiresAt == nil || !event.ExpiresAt.Equal(expiresAt.UTC().Truncate(time.Millisecond)) {
		t.Errorf("ExpiresAt = %v, want %v", event.ExpiresAt, expiresAt.UTC())
	}
	if event.CancelAtPeriodEnd == nil || *event.CancelAtPeriodEnd {
		t.Errorf("CancelAtPeriodEnd = %v, want false", event.CancelAtPeriodEnd)
	}
}

// CANCELLATION means auto-renew was turned off; the event must say so.
func TestWebhookCallback_CancellationState(t *testing.T) {
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)
	payload.Event.Type = "CANCELLATION"
	payload.Event.CancelReason = "UNSUBSCRIBE"
	payload.Event.ExpirationAtMs = eventTime.AddDate(0, 1, 0).UnixMilli()

	event := captureBillingEvent(t, payload)

	if event.CancelAtPeriodEnd == nil || !*event.CancelAtPeriodEnd {
		t.Fatalf("CancelAtPeriodEnd = %v, want true", event.CancelAtPeriodEnd)
	}
	if event.CancelReason != "UNSUBSCRIBE" {
		t.Errorf("CancelReason = %q, want UNSUBSCRIBE", event.CancelReason)
	}
}

// A refund-triggered CANCELLATION must not claim auto-renew was stopped: the
// subscription can still renew (RevenueCat docs).
func TestWebhookCallback_CancellationStateRefundStaysUnknown(t *testing.T) {
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)
	payload.Event.Type = "CANCELLATION"
	payload.Event.CancelReason = "CUSTOMER_SUPPORT"
	payload.Event.ExpirationAtMs = eventTime.AddDate(0, 1, 0).UnixMilli()

	event := captureBillingEvent(t, payload)

	if event.CancelAtPeriodEnd != nil {
		t.Fatalf("CancelAtPeriodEnd = %v, want nil", *event.CancelAtPeriodEnd)
	}
}

// RevenueCat's `price` is USD; when the purchase-currency amount is absent the
// pair must not mix a non-USD `currency` with a USD amount.
func TestWebhookCallback_PriceFallsBackToUSD(t *testing.T) {
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)
	payload.Event.Currency = "EUR"
	payload.Event.Price = 49.99 // USD
	payload.Event.PriceInPurchasedCurrency = 0

	event := captureBillingEvent(t, payload)

	if event.PriceCents != 4999 {
		t.Errorf("PriceCents = %d, want 4999", event.PriceCents)
	}
	if event.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", event.Currency)
	}
}

// An unrelated event must not claim to change cancellation state.
func TestWebhookCallback_CancellationStateUnknown(t *testing.T) {
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)
	payload.Event.Type = "BILLING_ISSUE"
	payload.Event.ExpirationAtMs = eventTime.AddDate(0, 1, 0).UnixMilli()

	event := captureBillingEvent(t, payload)

	if event.CancelAtPeriodEnd != nil {
		t.Fatalf("CancelAtPeriodEnd = %v, want nil", *event.CancelAtPeriodEnd)
	}
}
