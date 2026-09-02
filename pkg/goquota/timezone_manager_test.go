package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

type fixedNowStorage struct {
	*memory.Storage
	now time.Time
}

func (s *fixedNowStorage) Now(_ context.Context) (time.Time, error) {
	return s.now, nil
}

func TestManager_GetQuota_DailyUsesEntitlementTimezone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	fixedNow := time.Date(2026, 9, 3, 1, 43, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     fixedNow.UTC(),
	}
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": 3,
				},
				ConsumptionOrder: []goquota.PeriodType{goquota.PeriodTypeDaily},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "tz-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: fixedNow.UTC(),
		UpdatedAt:             fixedNow.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	usage, err := manager.GetQuota(ctx, userID, "receipt_scan", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}

	wantEnd := time.Date(2026, 9, 4, 0, 0, 0, 0, loc).UTC()
	if !usage.Period.End.Equal(wantEnd) {
		t.Fatalf("period end = %v, want %v", usage.Period.End, wantEnd)
	}
}

func TestManager_UpdateTimezone_InvalidTimezone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {Name: "free"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = manager.UpdateTimezone(ctx, "user1", "Invalid/Zone")
	if err != goquota.ErrInvalidTimezone {
		t.Fatalf("err = %v, want ErrInvalidTimezone", err)
	}
}

func TestManager_UpdateTimezone_PersistsOnEntitlement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {Name: "free"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "user-tz"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	if err := manager.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone: %v", err)
	}

	ent, err := manager.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("GetEntitlement: %v", err)
	}
	if ent.Timezone != "Europe/Bucharest" {
		t.Fatalf("timezone = %q, want Europe/Bucharest", ent.Timezone)
	}

	if err := manager.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone idempotent: %v", err)
	}
}

func TestManager_Consume_ResetsAtUserLocalMidnight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	beforeMidnight := time.Date(2026, 9, 3, 23, 30, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     beforeMidnight.UTC(),
	}
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": 1,
				},
				ConsumptionOrder: []goquota.PeriodType{goquota.PeriodTypeDaily},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "consume-tz-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: beforeMidnight.UTC(),
		UpdatedAt:             beforeMidnight.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	if _, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	_, err = manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily)
	if err != goquota.ErrQuotaExceeded {
		t.Fatalf("expected quota exceeded before local reset, got %v", err)
	}

	afterMidnight := time.Date(2026, 9, 4, 0, 30, 0, 0, loc)
	storage.now = afterMidnight.UTC()

	if _, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume after local midnight: %v", err)
	}
}
