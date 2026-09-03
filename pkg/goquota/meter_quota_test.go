package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMeterQuota_UnusedBonusWidensTheBar(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)
	userID := "meter-unused"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_chat", 5,
		goquota.WithTopUpIdempotencyKey("bonus-unused")))

	meter, err := manager.GetMeterQuota(ctx, userID, "receipt_chat")
	require.NoError(t, err)
	require.NotNil(t, meter)
	assert.Equal(t, 0, meter.Used)
	assert.Equal(t, 10, meter.Limit)
	assert.Equal(t, 10, meter.Remaining)
	require.Len(t, meter.Periods, 2)
	assert.Equal(t, goquota.PeriodTypeDaily, meter.Periods[0].Period)
	assert.Equal(t, 0, meter.Periods[0].Used)
	assert.Equal(t, 5, meter.Periods[0].Limit)
	assert.Equal(t, 5, meter.Periods[0].Remaining)
	assert.Equal(t, goquota.PeriodTypeForever, meter.Periods[1].Period)
	assert.Equal(t, 0, meter.Periods[1].Used)
	assert.Equal(t, 5, meter.Periods[1].Limit)
	assert.Equal(t, 5, meter.Periods[1].Remaining)
}

func TestGetMeterQuota_SpentBonusDropsOffTheBar(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)
	userID := "meter-spent"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_chat", 5,
		goquota.WithTopUpIdempotencyKey("bonus-spent")))

	for i := 0; i < 5; i++ {
		_, err := manager.Consume(ctx, userID, "receipt_chat", 1, goquota.PeriodTypeForever)
		require.NoError(t, err)
	}

	meter, err := manager.GetMeterQuota(ctx, userID, "receipt_chat")
	require.NoError(t, err)
	require.NotNil(t, meter)
	assert.Equal(t, 0, meter.Used)
	assert.Equal(t, 5, meter.Limit)
	assert.Equal(t, 5, meter.Remaining)

	require.Len(t, meter.Periods, 2)
	assert.Equal(t, goquota.PeriodTypeDaily, meter.Periods[0].Period)
	assert.Equal(t, 0, meter.Periods[0].Used)
	assert.Equal(t, 5, meter.Periods[0].Remaining)
	assert.Equal(t, goquota.PeriodTypeForever, meter.Periods[1].Period)
	assert.Equal(t, 5, meter.Periods[1].Used)
	assert.Equal(t, 5, meter.Periods[1].Limit)
	assert.Equal(t, 0, meter.Periods[1].Remaining)
}

func TestGetMeterQuota_PartialBonusAfterDailyExhausted(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)
	userID := "meter-partial"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 5,
		goquota.WithTopUpIdempotencyKey("bonus-partial")))

	_, err := manager.Consume(ctx, userID, "receipt_scan", 5, goquota.PeriodTypeDaily)
	require.NoError(t, err)
	_, err = manager.Consume(ctx, userID, "receipt_scan", 2, goquota.PeriodTypeForever)
	require.NoError(t, err)

	meter, err := manager.GetMeterQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, 5, meter.Used)
	assert.Equal(t, 8, meter.Limit)
	assert.Equal(t, 3, meter.Remaining)
}

func TestGetMeterQuota_DoesNotChangeLedgerEffectiveQuota(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 3)
	userID := "meter-vs-ledger"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 2,
		goquota.WithTopUpIdempotencyKey("ledger-bonus")))
	_, err := manager.Consume(ctx, userID, "receipt_scan", 3, goquota.PeriodTypeDaily)
	require.NoError(t, err)
	_, err = manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeForever)
	require.NoError(t, err)

	eff, err := manager.GetEffectiveQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, 4, eff.Used)
	assert.Equal(t, 5, eff.Limit)
	assert.Equal(t, 1, eff.Remaining)

	meter, err := manager.GetMeterQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, 3, meter.Used)
	assert.Equal(t, 4, meter.Limit)
	assert.Equal(t, 1, meter.Remaining)
}

func TestGetMeterQuota_UnlimitedShortCircuits(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 3)
	userID := "meter-unlimited"
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "premium",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, userID, "receipt_scan", 10,
		goquota.WithTopUpIdempotencyKey("prem-meter")))

	meter, err := manager.GetMeterQuota(ctx, userID, "receipt_scan")
	require.NoError(t, err)
	assert.Equal(t, -1, meter.Limit)
	assert.Equal(t, -1, meter.Remaining)
}

func TestGetMeterQuota_SkipsZeroConfiguredPeriods(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)

	meter, err := manager.GetMeterQuota(ctx, "no-bonus-user", "receipt_chat")
	require.NoError(t, err)
	assert.Equal(t, 0, meter.Used)
	assert.Equal(t, 5, meter.Limit)
	assert.Equal(t, 5, meter.Remaining)
	require.Len(t, meter.Periods, 1)
	assert.Equal(t, goquota.PeriodTypeDaily, meter.Periods[0].Period)
}

func TestGetMeterQuota_IncludesOrphanForeverOutsideConsumptionOrder(t *testing.T) {
	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "orphan-meter",
		Tier:   "free",
	}))
	require.NoError(t, manager.TopUpLimit(ctx, "orphan-meter", "orphaned_resource", 500,
		goquota.WithTopUpIdempotencyKey("orphan-bonus")))

	meter, err := manager.GetMeterQuota(ctx, "orphan-meter", "orphaned_resource")
	require.NoError(t, err)
	assert.Equal(t, 0, meter.Used)
	assert.Equal(t, 500, meter.Limit)
	assert.Equal(t, 500, meter.Remaining)
	require.Len(t, meter.Periods, 1)
	assert.Equal(t, goquota.PeriodTypeForever, meter.Periods[0].Period)
	assert.Equal(t, 500, meter.Periods[0].Remaining)
}

func TestGetMeterQuota_RejectsEmptyInputsAndCanceledContext(t *testing.T) {
	manager := newOverflowTestManager(t, 5)
	_, err := manager.GetMeterQuota(context.Background(), "", "receipt_chat")
	assert.Error(t, err)
	_, err = manager.GetMeterQuota(context.Background(), "u1", "")
	assert.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = manager.GetMeterQuota(ctx, "u1", "receipt_chat")
	assert.ErrorIs(t, err, context.Canceled)
}
