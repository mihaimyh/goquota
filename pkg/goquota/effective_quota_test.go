package goquota_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOverflowTestManager(t *testing.T, dailyLimit int) *goquota.Manager {
	t.Helper()
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": dailyLimit,
					"receipt_chat": 5,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeDaily,
					goquota.PeriodTypeForever,
				},
			},
			"premium": {
				Name: "premium",
				DailyQuotas: map[string]int{
					"receipt_scan": -1,
					"receipt_chat": 50,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeDaily,
					goquota.PeriodTypeForever,
				},
			},
		},
	}
	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)
	return manager
}

func TestConsumeWithResult_ReportsChargedPeriod_AutoCascade(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 2)
	userID := "auto-period-user"

	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 1,
		goquota.WithTopUpIdempotencyKey("bonus-"+userID)))

	// First two consume from daily.
	for i := 0; i < 2; i++ {
		result, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto,
			goquota.WithIdempotencyKey(fmt.Sprintf("daily-%d", i)))
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, goquota.PeriodTypeDaily, result.Period)
		assert.GreaterOrEqual(t, result.NewUsed, 1)
		assert.Equal(t, 2, result.Limit)
	}

	// Third consumes forever overflow.
	result, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto,
		goquota.WithIdempotencyKey("forever-1"))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, goquota.PeriodTypeForever, result.Period)
	assert.Equal(t, 1, result.NewUsed)
	assert.Equal(t, 1, result.Limit)
	assert.Equal(t, 0, result.Remaining)
}

func TestConsumeWithResult_IdempotentReplayPreservesPeriod(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 1)
	userID := "idem-period-user"

	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 1,
		goquota.WithTopUpIdempotencyKey("bonus-idem")))

	// Exhaust daily so Auto lands on forever.
	_, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily,
		goquota.WithIdempotencyKey("exhaust-daily"))
	require.NoError(t, err)

	first, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto,
		goquota.WithIdempotencyKey("same-key"))
	require.NoError(t, err)
	assert.Equal(t, goquota.PeriodTypeForever, first.Period)

	replay, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto,
		goquota.WithIdempotencyKey("same-key"))
	require.NoError(t, err)
	assert.Equal(t, first.Period, replay.Period)
	assert.Equal(t, first.NewUsed, replay.NewUsed)
	assert.Equal(t, first.Limit, replay.Limit)
}

func TestConsumeWithResult_ExplicitPeriodField(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 10)
	userID := "explicit-period-user"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))

	result, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 2, goquota.PeriodTypeDaily)
	require.NoError(t, err)
	assert.Equal(t, goquota.PeriodTypeDaily, result.Period)
	assert.Equal(t, 2, result.NewUsed)
	assert.Equal(t, 10, result.Limit)
	assert.Equal(t, 8, result.Remaining)
}

func TestConsumeWithResult_UnlimitedPeriod(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 3)
	userID := "unlimited-user"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "premium",
	}))

	result, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto)
	require.NoError(t, err)
	assert.Equal(t, goquota.PeriodTypeDaily, result.Period)
	assert.Equal(t, -1, result.Limit)
	assert.Equal(t, -1, result.Remaining)
}

func TestRefundFromConsumeResult_UsesChargedPeriod(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 1)
	userID := "refund-from-result"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 1,
		goquota.WithTopUpIdempotencyKey("bonus-refund")))

	_, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily,
		goquota.WithIdempotencyKey("d1"))
	require.NoError(t, err)

	consumeKey := "overflow-consume"
	result, err := manager.ConsumeWithResult(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeAuto,
		goquota.WithIdempotencyKey(consumeKey))
	require.NoError(t, err)
	assert.Equal(t, goquota.PeriodTypeForever, result.Period)

	require.NoError(t, manager.RefundFromConsume(ctx, &goquota.RefundFromConsumeRequest{
		UserID:         userID,
		Resource:       "receipt_scan",
		Amount:         1,
		ConsumeResult:  result,
		ConsumeIdemKey: consumeKey,
		Reason:         "ai_failed",
	}))

	forever, err := manager.GetQuota(ctx, userID, "receipt_scan", goquota.PeriodTypeForever)
	require.NoError(t, err)
	assert.Equal(t, 0, forever.Used)
}

func TestGetEffectiveQuota_MergesDailyAndForever(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 3)
	userID := "effective-user"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 2,
		goquota.WithTopUpIdempotencyKey("eff-bonus")))

	eff, err := manager.GetEffectiveQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, 0, eff.Used)
	assert.Equal(t, 5, eff.Limit) // 3 daily + 2 forever
	assert.Equal(t, 5, eff.Remaining)

	_, err = manager.Consume(ctx, userID, "receipt_scan", 3, goquota.PeriodTypeDaily)
	require.NoError(t, err)
	_, err = manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeForever)
	require.NoError(t, err)

	eff, err = manager.GetEffectiveQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, 4, eff.Used)      // 3 daily + 1 forever
	assert.Equal(t, 5, eff.Limit)     // stable
	assert.Equal(t, 1, eff.Remaining) // 1 forever left
}

func TestGetEffectiveQuota_UnlimitedShortCircuits(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 3)
	userID := "eff-unlimited"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "premium",
	}))
	// Forever credits should not matter when daily is unlimited.
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 10,
		goquota.WithTopUpIdempotencyKey("prem-bonus")))

	eff, err := manager.GetEffectiveQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, -1, eff.Limit)
	assert.Equal(t, -1, eff.Remaining)
}

func TestGetEffectiveQuota_SkipsZeroConfiguredPeriods(t *testing.T) {
	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"api_calls": 10,
				},
				MonthlyQuotas: map[string]int{
					"api_calls": 0, // not granted
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeMonthly,
					goquota.PeriodTypeDaily,
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "u-zero",
		Tier:   "free",
	}))

	eff, err := manager.GetEffectiveQuota(ctx, "u-zero", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, 10, eff.Limit)
	assert.Equal(t, 0, eff.Used)
	assert.Equal(t, 10, eff.Remaining)
}

func TestGetEffectiveQuota_EmptyUserID(t *testing.T) {
	manager := newOverflowTestManager(t, 3)
	_, err := manager.GetEffectiveQuota(context.Background(), "", "receipt_scan")
	assert.Error(t, err)
}

func TestGetEffectiveQuota_EmptyResource(t *testing.T) {
	manager := newOverflowTestManager(t, 3)
	_, err := manager.GetEffectiveQuota(context.Background(), "u1", "")
	assert.Error(t, err)
}

func TestRefundFromConsume_NilResult(t *testing.T) {
	manager := newOverflowTestManager(t, 3)
	err := manager.RefundFromConsume(context.Background(), &goquota.RefundFromConsumeRequest{
		UserID:   "u1",
		Resource: "receipt_scan",
		Amount:   1,
	})
	assert.Error(t, err)
}
