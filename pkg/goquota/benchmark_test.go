package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func BenchmarkManager_Consume(b *testing.B) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "pro",
		Tiers: map[string]goquota.TierConfig{
			"pro": {
				MonthlyQuotas: map[string]int{"api_calls": 100000},
			},
		},
	}

	manager, _ := goquota.NewManager(storage, config)
	ctx := context.Background()

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "bench_user",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.Consume(ctx, "bench_user", "api_calls", 1, goquota.PeriodTypeMonthly)
	}
}

func BenchmarkManager_GetQuota(b *testing.B) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "pro",
		Tiers: map[string]goquota.TierConfig{
			"pro": {
				MonthlyQuotas: map[string]int{"api_calls": 100000},
			},
		},
	}

	manager, _ := goquota.NewManager(storage, config)
	ctx := context.Background()

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "bench_user",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.GetQuota(ctx, "bench_user", "api_calls", goquota.PeriodTypeMonthly)
	}
}

func BenchmarkManager_GetCurrentCycle(b *testing.B) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "pro",
		Tiers: map[string]goquota.TierConfig{
			"pro": {MonthlyQuotas: map[string]int{"api_calls": 100000}},
		},
	}

	manager, _ := goquota.NewManager(storage, config)
	ctx := context.Background()

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "bench_user",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.GetCurrentCycle(ctx, "bench_user")
	}
}

func BenchmarkStorage_ConsumeQuota(b *testing.B) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "bench_user",
		Resource: "api_calls",
		Amount:   1,
		Tier:     "pro",
		Period:   period,
		Limit:    100000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.ConsumeQuota(ctx, req)
	}
}

func BenchmarkManager_ApplyTierChange(b *testing.B) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {MonthlyQuotas: map[string]int{"api_calls": 100}},
			"pro":  {MonthlyQuotas: map[string]int{"api_calls": 10000}},
		},
	}

	manager, _ := goquota.NewManager(storage, config)
	ctx := context.Background()

	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "bench_user",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.ApplyTierChange(ctx, "bench_user", "free", "pro", "api_calls")
		_ = manager.ApplyTierChange(ctx, "bench_user", "pro", "free", "api_calls")
	}
}
