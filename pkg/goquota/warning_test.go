package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

type mockWarningHandler struct {
	warnings []struct {
		usage     *goquota.Usage
		threshold float64
	}
}

func (h *mockWarningHandler) OnWarning(_ context.Context, usage *goquota.Usage, threshold float64) {
	h.warnings = append(h.warnings, struct {
		usage     *goquota.Usage
		threshold float64
	}{usage, threshold})
}

func TestManager_Warnings(t *testing.T) {
	storage := memory.New()
	handler := &mockWarningHandler{}

	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5, 0.8, 0.9},
				},
			},
		},
		WarningHandler: handler,
	}

	manager, _ := goquota.NewManager(storage, &config)
	ctx := context.Background()
	userID := testUserID1

	// Set up entitlement
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// 1. Consume 40% (no warning)
	_, _ = manager.Consume(ctx, userID, "api_calls", 40, goquota.PeriodTypeMonthly)
	if len(handler.warnings) != 0 {
		t.Errorf("Expected 0 warnings, got %d", len(handler.warnings))
	}

	// 2. Consume 15% more (crosses 0.5 threshold)
	_, _ = manager.Consume(ctx, userID, "api_calls", 15, goquota.PeriodTypeMonthly)
	if len(handler.warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(handler.warnings))
	} else if handler.warnings[0].threshold != 0.5 {
		t.Errorf("Expected 0.5 threshold, got %f", handler.warnings[0].threshold)
	}

	// 3. Consume 5% more (does not cross next threshold 0.8)
	_, _ = manager.Consume(ctx, userID, "api_calls", 5, goquota.PeriodTypeMonthly)
	if len(handler.warnings) != 1 {
		t.Errorf("Expected still 1 warning, got %d", len(handler.warnings))
	}

	// 4. Consume 25% more (crosses 0.8 threshold)
	_, _ = manager.Consume(ctx, userID, "api_calls", 25, goquota.PeriodTypeMonthly)
	if len(handler.warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d", len(handler.warnings))
	} else if handler.warnings[1].threshold != 0.8 {
		t.Errorf("Expected 0.8 threshold, got %f", handler.warnings[1].threshold)
	}
}

func TestManager_Warnings_LargeJump(t *testing.T) {
	storage := memory.New()
	handler := &mockWarningHandler{}

	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5, 0.8},
				},
			},
		},
		WarningHandler: handler,
	}

	manager, _ := goquota.NewManager(storage, &config)
	ctx := context.Background()
	userID := testUserID1

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Consume 85% in one go (crosses BOTH 0.5 and 0.8)
	_, _ = manager.Consume(ctx, userID, "api_calls", 85, goquota.PeriodTypeMonthly)

	// Should trigger TWO warnings
	if len(handler.warnings) != 2 {
		t.Errorf("Expected 2 warnings for large jump, got %d", len(handler.warnings))
	}
}

func TestManager_ContextWarningHandler(t *testing.T) {
	storage := memory.New()
	globalHandler := &mockWarningHandler{}
	ctxHandler := &mockWarningHandler{}

	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5},
				},
			},
		},
		WarningHandler: globalHandler,
	}

	manager, _ := goquota.NewManager(storage, &config)
	ctx := context.Background()
	userID := "user1"

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Add context handler
	ctx = goquota.WithWarningHandler(ctx, ctxHandler)

	// Consume 60% (crosses 0.5)
	_, _ = manager.Consume(ctx, userID, "api_calls", 60, goquota.PeriodTypeMonthly)

	// Both should be called
	if len(globalHandler.warnings) != 1 {
		t.Errorf("Global handler not called")
	}
	if len(ctxHandler.warnings) != 1 {
		t.Errorf("Context handler not called")
	}
}
