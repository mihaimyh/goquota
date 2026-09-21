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
