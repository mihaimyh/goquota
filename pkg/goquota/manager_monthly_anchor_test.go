package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// clockStorage is an in-memory storage whose Now() can be moved by the test.
type clockStorage struct {
	*memory.Storage
	now time.Time
}

func (s *clockStorage) Now(_ context.Context) (time.Time, error) {
	return s.now, nil
}

func newMonthlyAnchorManager(t *testing.T, clock *clockStorage) *goquota.Manager {
	t.Helper()

	manager, err := goquota.NewManager(clock, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

// TestManager_Consume_MonthlyWithoutEntitlementUsesCalendarMonth guards QM-01:
// the monthly fallback used to anchor on the start of the current day, so the
// period key changed daily and the quota reset every day.
func TestManager_Consume_MonthlyWithoutEntitlementUsesCalendarMonth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := &clockStorage{
		Storage: memory.New(),
		now:     time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC),
	}
	manager := newMonthlyAnchorManager(t, clock)
	userID := "no-entitlement-user"

	used, err := manager.Consume(ctx, userID, "api_calls", 1, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("consume on the 3rd: %v", err)
	}
	if used != 1 {
		t.Fatalf("used after first consume = %d, want 1", used)
	}

	// Same calendar month, different day: usage must accumulate.
	clock.now = time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	used, err = manager.Consume(ctx, userID, "api_calls", 1, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("consume on the 28th: %v", err)
	}
	if used != 2 {
		t.Fatalf("used after second consume = %d, want 2 (monthly quota reset daily)", used)
	}

	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if usage.Used != 2 {
		t.Fatalf("GetQuota used = %d, want 2", usage.Used)
	}
	wantPeriodStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !usage.Period.Start.Equal(wantPeriodStart) {
		t.Fatalf("period start = %v, want %v", usage.Period.Start, wantPeriodStart)
	}

	// The next calendar month starts a fresh period.
	clock.now = time.Date(2026, 4, 1, 0, 5, 0, 0, time.UTC)
	used, err = manager.Consume(ctx, userID, "api_calls", 1, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("consume on the 1st of the next month: %v", err)
	}
	if used != 1 {
		t.Fatalf("used in the new month = %d, want 1", used)
	}
}
