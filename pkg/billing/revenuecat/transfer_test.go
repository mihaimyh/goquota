package revenuecat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestFilterAppUserIDs_SkipsRevenueCatAnonymous(t *testing.T) {
	got := filterAppUserIDs([]string{
		"$RCAnonymousID:55ec31deddd44def88cdb99fdf85009c",
		"RyPO5L6jmjUbNLME44ZihFuXDgh1",
		"  ",
		"$RCanonymousID:mixed-case",
		"n74fZV8LdeabJWct0R6CGTbclyi1",
	})
	if len(got) != 2 {
		t.Fatalf("filterAppUserIDs = %#v, want 2 firebase uids", got)
	}
	if got[0] != "RyPO5L6jmjUbNLME44ZihFuXDgh1" || got[1] != "n74fZV8LdeabJWct0R6CGTbclyi1" {
		t.Fatalf("filterAppUserIDs = %#v", got)
	}
}

func TestProvider_Webhook_Transfer_MissingAppUserID_MovesEntitlement(t *testing.T) {
	ctx := context.Background()
	manager := mockManager(t)
	sourceID := "RyPO5L6jmjUbNLME44ZihFuXDgh1"
	destID := "n74fZV8LdeabJWct0R6CGTbclyi1"
	expires := time.Now().UTC().Add(24 * time.Hour)

	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                sourceID,
		Tier:                  testTierScholar,
		SubscriptionStartDate: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		ExpiresAt:             &expires,
		UpdatedAt:             time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed source entitlement: %v", err)
	}

	var callbacks []billing.WebhookEvent
	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"scholar_monthly": testTierScholar,
			"*":               testTierExplorer,
		},
		WebhookSecret: testSecret,
		WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
			callbacks = append(callbacks, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"api_version": "1.0",
		"event": map[string]any{
			"id":                    "BC96ECDA-ED24-453C-8A70-0A81DCD97390",
			"type":                  "TRANSFER",
			"event_timestamp_ms":    time.Now().UnixMilli(),
			"transferred_from":      []string{"$RCAnonymousID:55ec31deddd44def88cdb99fdf85009c", sourceID},
			"transferred_to":        []string{destID, "$RCAnonymousID:78170651f69e4f9aaf045a3919a3945f"},
			"environment":           "SANDBOX",
			"store":                 "PLAY_STORE",
			"subscriber_attributes": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("Authorization", testBearerSecret)
	w := httptest.NewRecorder()
	provider.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TRANSFER status=%d body=%q, want 200 (incident was 400 missing user id)", w.Code, w.Body.String())
	}

	source, err := manager.GetEntitlement(ctx, sourceID)
	if err != nil {
		t.Fatalf("source entitlement: %v", err)
	}
	if source.Tier != testTierExplorer {
		t.Fatalf("source tier=%q, want default %q after transfer away", source.Tier, testTierExplorer)
	}

	dest, err := manager.GetEntitlement(ctx, destID)
	if err != nil {
		t.Fatalf("dest entitlement: %v", err)
	}
	if dest.Tier != testTierScholar {
		t.Fatalf("dest tier=%q, want copied %q", dest.Tier, testTierScholar)
	}
	if dest.ExpiresAt == nil || !dest.ExpiresAt.Equal(expires) {
		t.Fatalf("dest ExpiresAt=%v, want %v", dest.ExpiresAt, expires)
	}

	if len(callbacks) < 2 {
		t.Fatalf("callbacks=%d, want source downgrade + dest grant", len(callbacks))
	}
	sawSourceFree, sawDestPaid := false, false
	for _, ev := range callbacks {
		if ev.EventType != "TRANSFER" {
			t.Fatalf("callback EventType=%q, want TRANSFER", ev.EventType)
		}
		if ev.UserID == sourceID && ev.NewTier == testTierExplorer {
			sawSourceFree = true
		}
		if ev.UserID == destID && ev.NewTier == testTierScholar {
			sawDestPaid = true
		}
	}
	if !sawSourceFree || !sawDestPaid {
		t.Fatalf("callbacks missing expected tier writes: %+v", callbacks)
	}
}

func TestProvider_Webhook_Transfer_OnlyAnonymous_OKNoOp(t *testing.T) {
	provider, err := NewProvider(billing.Config{
		Manager:       mockManager(t),
		WebhookSecret: testSecret,
		TierMapping:   map[string]string{"*": testTierExplorer},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"event": map[string]any{
			"type":               "TRANSFER",
			"event_timestamp_ms": time.Now().UnixMilli(),
			"transferred_from":   []string{"$RCAnonymousID:aaa"},
			"transferred_to":     []string{"$RCAnonymousID:bbb"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("Authorization", testBearerSecret)
	w := httptest.NewRecorder()
	provider.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("anonymous-only TRANSFER status=%d body=%q, want 200 no-op", w.Code, w.Body.String())
	}
}

func TestProvider_Webhook_InitialPurchase_StillRequiresAppUserID(t *testing.T) {
	provider, err := NewProvider(billing.Config{
		Manager:       mockManager(t),
		WebhookSecret: testSecret,
		TierMapping:   map[string]string{"*": testTierExplorer},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"event": map[string]any{
			"type": "INITIAL_PURCHASE",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("Authorization", testBearerSecret)
	w := httptest.NewRecorder()
	provider.handleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("INITIAL_PURCHASE without app_user_id status=%d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing user id") {
		t.Fatalf("body=%q, want missing user id", w.Body.String())
	}
}
