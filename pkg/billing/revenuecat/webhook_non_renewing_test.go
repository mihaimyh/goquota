package revenuecat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// A one-time purchase (consumable/non-consumable) is not a subscription event.
// It must never rewrite the subscription entitlement: refunds and top-ups would
// otherwise silently downgrade a premium subscriber to the default tier.
func TestNonRenewingPurchase_DoesNotTouchEntitlement(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	seedTime := time.Now().Add(-time.Hour).UTC()
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "buyer",
		Tier:                  testTierScholar,
		SubscriptionStartDate: seedTime,
		UpdatedAt:             seedTime,
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	var captured *billing.WebhookEvent
	provider, err := NewProvider(billing.Config{
		Manager:       manager,
		TierMapping:   map[string]string{"premium": testTierScholar},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
			captured = &event
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	payload := createTestPayload("buyer", "premium", time.Now())
	payload.Event.Type = "NON_RENEWING_PURCHASE"
	payload.Event.ProductID = "scan_pack_10"
	payload.Event.EntitlementID = ""
	payload.Event.EntitlementIDs = nil
	payload.Event.Currency = "EUR"
	payload.Event.PriceInPurchasedCurrency = 1.99

	if err := provider.processWebhookEvent(ctx, &payload, "buyer"); err != nil {
		t.Fatalf("processWebhookEvent: %v", err)
	}

	ent, err := manager.GetEntitlement(ctx, "buyer")
	if err != nil {
		t.Fatalf("GetEntitlement: %v", err)
	}
	if ent.Tier != testTierScholar {
		t.Errorf("tier = %q, want %q: a one-time purchase must not change the tier", ent.Tier, testTierScholar)
	}
	if !ent.UpdatedAt.Equal(seedTime) {
		t.Errorf("UpdatedAt = %v, want %v: a one-time purchase must not bump it", ent.UpdatedAt, seedTime)
	}

	if captured == nil {
		t.Fatal("webhook callback was not invoked for a one-time purchase")
	}
	if captured.EventType != "NON_RENEWING_PURCHASE" {
		t.Errorf("EventType = %q, want NON_RENEWING_PURCHASE", captured.EventType)
	}
	if captured.ProductID != "scan_pack_10" {
		t.Errorf("ProductID = %q, want scan_pack_10", captured.ProductID)
	}
	if captured.PriceCents != 199 {
		t.Errorf("PriceCents = %d, want 199", captured.PriceCents)
	}
	if captured.NewTier != testTierScholar {
		t.Errorf("NewTier = %q, want %q (unchanged)", captured.NewTier, testTierScholar)
	}
}

// A one-time purchase must not create a subscription entitlement for a free user
// either: that would start a phantom subscription with an invented expiry.
func TestNonRenewingPurchase_DoesNotCreateEntitlement(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	provider, err := NewProvider(billing.Config{
		Manager:       manager,
		TierMapping:   map[string]string{"premium": testTierScholar},
		WebhookSecret: "test_secret",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	payload := createTestPayload("newbie", "", time.Now())
	payload.Event.Type = "NON_RENEWING_PURCHASE"
	payload.Event.ProductID = "scan_pack_10"
	payload.Event.EntitlementID = ""
	payload.Event.EntitlementIDs = nil
	payload.Event.PriceInPurchasedCurrency = 1.99

	if err := provider.processWebhookEvent(ctx, &payload, "newbie"); err != nil {
		t.Fatalf("processWebhookEvent: %v", err)
	}

	if _, err := manager.GetEntitlement(ctx, "newbie"); !errors.Is(err, goquota.ErrEntitlementNotFound) {
		t.Errorf("GetEntitlement err = %v, want ErrEntitlementNotFound (no entitlement must be created)", err)
	}
}
