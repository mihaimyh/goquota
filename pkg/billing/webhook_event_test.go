package billing

import (
	"testing"
	"time"
)

func TestDeriveBillingPeriod(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name               string
		purchased, expires time.Time
		want               string
	}{
		{"annual", start, start.AddDate(1, 0, 0), PeriodAnnual},
		{"monthly", start, start.AddDate(0, 1, 0), PeriodMonthly},
		{"week", start, start.AddDate(0, 0, 7), ""},
		{"three-day trial", start, start.AddDate(0, 0, 3), ""},
		{"zero purchase", time.Time{}, start.AddDate(1, 0, 0), ""},
		{"zero expiry", start, time.Time{}, ""},
		{"expiry before purchase", start, start.Add(-time.Hour), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveBillingPeriod(tc.purchased, tc.expires); got != tc.want {
				t.Fatalf("DeriveBillingPeriod() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWebhookEvent_ProviderObjectIdentifiers pins the provider object ids that
// let a consumer match a webhook to the purchase it reports.
func TestWebhookEvent_ProviderObjectIdentifiers(t *testing.T) {
	tests := []struct {
		name         string
		event        WebhookEvent
		wantObjectID string
		wantIntentID string
	}{
		{
			name: "checkout session",
			event: WebhookEvent{
				EventID:          "evt_1",
				ProviderObjectID: "cs_test_123",
				PaymentIntentID:  "pi_test_123",
			},
			wantObjectID: "cs_test_123",
			wantIntentID: "pi_test_123",
		},
		{
			name: "charge refund without a payment intent",
			event: WebhookEvent{
				EventID:          "evt_2",
				ProviderObjectID: "ch_test_123",
			},
			wantObjectID: "ch_test_123",
			wantIntentID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.event.ProviderObjectID != tc.wantObjectID {
				t.Fatalf("ProviderObjectID = %q, want %q", tc.event.ProviderObjectID, tc.wantObjectID)
			}
			if tc.event.PaymentIntentID != tc.wantIntentID {
				t.Fatalf("PaymentIntentID = %q, want %q", tc.event.PaymentIntentID, tc.wantIntentID)
			}
		})
	}
}
