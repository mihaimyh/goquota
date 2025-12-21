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
_ = manager.Consume(ctx, "bench_user", "api_calls", 1, goquota.PeriodTypeMonthly)
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

func BenchmarkCycleCalculation(b *testing.B) {
start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
now := time.Date(2024, 6, 20, 12, 30, 0, 0, time.UTC)

b.ResetTimer()
for i := 0; i < b.N; i++ {
_, _ = goquota.CurrentCycleForStart(start, now)
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
_ = storage.ConsumeQuota(ctx, req)
}
}