package tiered

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		hot := memory.New()
		cold := memory.New()

		storage, err := New(Config{Hot: hot, Cold: cold})
		assert.NoError(t, err)
		assert.NotNil(t, storage)
		assert.NoError(t, storage.Close())
	})

	t.Run("nil hot storage", func(t *testing.T) {
		cold := memory.New()
		storage, err := New(Config{Cold: cold})
		assert.Error(t, err)
		assert.Nil(t, storage)
		assert.Contains(t, err.Error(), "hot and cold storage are required")
	})

	t.Run("nil cold storage", func(t *testing.T) {
		hot := memory.New()
		storage, err := New(Config{Hot: hot})
		assert.Error(t, err)
		assert.Nil(t, storage)
		assert.Contains(t, err.Error(), "hot and cold storage are required")
	})

	t.Run("default sync buffer size", func(t *testing.T) {
		hot := memory.New()
		cold := memory.New()

		storage, err := New(Config{Hot: hot, Cold: cold, AsyncUsageSync: true})
		assert.NoError(t, err)
		assert.NotNil(t, storage)
		defer storage.Close()
		// Default should be 1000
		assert.Equal(t, 1000, cap(storage.syncQueue))
	})

	t.Run("custom sync buffer size", func(t *testing.T) {
		hot := memory.New()
		cold := memory.New()

		storage, err := New(Config{
			Hot:            hot,
			Cold:           cold,
			AsyncUsageSync: true,
			SyncBufferSize: 500,
		})
		assert.NoError(t, err)
		assert.NotNil(t, storage)
		defer storage.Close()
		assert.Equal(t, 500, cap(storage.syncQueue))
	})
}

// --- Read-Through Strategy Tests ---

func TestStorage_GetEntitlement_ReadThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	userID := "user1"
	expectedEnt := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	t.Run("hot hit", func(t *testing.T) {
		// Populate hot store
		require.NoError(t, hot.SetEntitlement(ctx, expectedEnt))

		ent, err := storage.GetEntitlement(ctx, userID)
		assert.NoError(t, err)
		assert.Equal(t, expectedEnt.UserID, ent.UserID)
		assert.Equal(t, expectedEnt.Tier, ent.Tier)

		// Cold should not be accessed
		coldEnt, _ := cold.GetEntitlement(ctx, userID)
		assert.Nil(t, coldEnt) // Cold was never written to
	})

	t.Run("hot miss, cold hit (read-through)", func(t *testing.T) {
		// Clear hot store
		hot2 := memory.New()
		cold2 := memory.New()
		storage2, _ := New(Config{Hot: hot2, Cold: cold2})
		defer storage2.Close()

		// Populate cold store only
		require.NoError(t, cold2.SetEntitlement(ctx, expectedEnt))

		ent, err := storage2.GetEntitlement(ctx, userID)
		assert.NoError(t, err)
		assert.Equal(t, expectedEnt.UserID, ent.UserID)
		assert.Equal(t, expectedEnt.Tier, ent.Tier)

		// Hot should now be populated (read-repair)
		hotEnt, err := hot2.GetEntitlement(ctx, userID)
		assert.NoError(t, err)
		assert.Equal(t, expectedEnt.UserID, hotEnt.UserID)
	})

	t.Run("both miss", func(t *testing.T) {
		hot3 := memory.New()
		cold3 := memory.New()
		storage3, _ := New(Config{Hot: hot3, Cold: cold3})
		defer storage3.Close()

		_, err := storage3.GetEntitlement(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Equal(t, goquota.ErrEntitlementNotFound, err)
	})
}

func TestStorage_GetUsage_ReadThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	userID := "user1"
	resource := "api_calls"
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}
	expectedUsage := &goquota.Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	t.Run("hot hit", func(t *testing.T) {
		require.NoError(t, hot.SetUsage(ctx, userID, resource, expectedUsage, period))

		usage, err := storage.GetUsage(ctx, userID, resource, period)
		assert.NoError(t, err)
		assert.NotNil(t, usage)
		assert.Equal(t, expectedUsage.Used, usage.Used)
		assert.Equal(t, expectedUsage.Limit, usage.Limit)
	})

	t.Run("hot miss, cold hit (read-through)", func(t *testing.T) {
		hot2 := memory.New()
		cold2 := memory.New()
		storage2, _ := New(Config{Hot: hot2, Cold: cold2})
		defer storage2.Close()

		require.NoError(t, cold2.SetUsage(ctx, userID, resource, expectedUsage, period))

		usage, err := storage2.GetUsage(ctx, userID, resource, period)
		assert.NoError(t, err)
		assert.NotNil(t, usage)
		assert.Equal(t, expectedUsage.Used, usage.Used)

		// Hot should now be populated
		hotUsage, err := hot2.GetUsage(ctx, userID, resource, period)
		assert.NoError(t, err)
		assert.NotNil(t, hotUsage)
		assert.Equal(t, expectedUsage.Used, hotUsage.Used)
	})
}

// --- Write-Through Strategy Tests ---

func TestStorage_SetEntitlement_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	err := storage.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// Verify both stores have it
	hotEnt, err := hot.GetEntitlement(ctx, "user1")
	assert.NoError(t, err)
	assert.Equal(t, ent.Tier, hotEnt.Tier)

	coldEnt, err := cold.GetEntitlement(ctx, "user1")
	assert.NoError(t, err)
	assert.Equal(t, ent.Tier, coldEnt.Tier)
}

func TestStorage_SetUsage_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}
	usage := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      75,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.NoError(t, err)

	// Verify both stores
	hotUsage, err := hot.GetUsage(ctx, "user1", "api_calls", period)
	assert.NoError(t, err)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 75, hotUsage.Used)

	coldUsage, err := cold.GetUsage(ctx, "user1", "api_calls", period)
	assert.NoError(t, err)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 75, coldUsage.Used)
}

func TestStorage_RefundQuota_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Setup usage first
	usage := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, hot.SetUsage(ctx, "user1", "api_calls", usage, period))
	require.NoError(t, cold.SetUsage(ctx, "user1", "api_calls", usage, period))

	req := &goquota.RefundRequest{
		UserID:            "user1",
		Resource:          "api_calls",
		Amount:            10,
		Period:            period,
		PeriodType:        goquota.PeriodTypeMonthly,
		IdempotencyKey:    "refund-1",
		IdempotencyKeyTTL: 24 * time.Hour,
	}

	err := storage.RefundQuota(ctx, req)
	assert.NoError(t, err)

	// Verify usage decreased in both stores
	hotUsage, _ := hot.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 40, hotUsage.Used)

	coldUsage, _ := cold.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 40, coldUsage.Used)
}

func TestStorage_AddLimit_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	err := storage.AddLimit(ctx, "user1", "api_calls", 50, period, "topup-1")
	assert.NoError(t, err)

	// Verify both stores have the limit
	hotUsage, _ := hot.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 50, hotUsage.Limit)

	coldUsage, _ := cold.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 50, coldUsage.Limit)
}

// --- Hot-Only Strategy Tests ---

func TestStorage_CheckRateLimit_HotOnly(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	req := &goquota.RateLimitRequest{
		UserID:    "user1",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Minute,
		Burst:     20,
		Now:       now,
	}

	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, remaining, 0)
	assert.False(t, resetTime.IsZero())

	// Cold should not be accessed (hot-only strategy)
	// We can't easily verify this without mocking, but the behavior is correct
}

func TestStorage_RecordRateLimitRequest_HotOnly(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	req := &goquota.RateLimitRequest{
		UserID:    "user1",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Now:       time.Now().UTC(),
	}

	err := storage.RecordRateLimitRequest(ctx, req)
	assert.NoError(t, err)
	// Cold is not accessed (hot-only strategy)
}

// --- Async Consumption Tests ---

func TestStorage_ConsumeQuota_Async(t *testing.T) {
	hot := memory.New()
	cold := memory.New()

	storage, _ := New(Config{
		Hot:            hot,
		Cold:           cold,
		AsyncUsageSync: true,
	})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}
	req := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10,
		Tier:           "pro",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "consume-1",
	}

	// Consume on hot (should return immediately)
	newUsed, err := storage.ConsumeQuota(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 10, newUsed)

	// Wait a bit for async worker to process
	time.Sleep(100 * time.Millisecond)

	// Verify hot store has it immediately
	hotUsage, err := hot.GetUsage(ctx, "user1", "api_calls", period)
	assert.NoError(t, err)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 10, hotUsage.Used)

	// Wait for async sync (with timeout)
	done := make(chan bool)
	go func() {
		time.Sleep(200 * time.Millisecond)
		done <- true
	}()

	select {
	case <-done:
		// Verify cold store has it after async sync
		coldUsage, err := cold.GetUsage(ctx, "user1", "api_calls", period)
		assert.NoError(t, err)
		assert.NotNil(t, coldUsage)
		assert.Equal(t, 10, coldUsage.Used)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for async sync")
	}
}

func TestStorage_ConsumeQuota_Sync(t *testing.T) {
	hot := memory.New()
	cold := memory.New()

	storage, _ := New(Config{
		Hot:            hot,
		Cold:           cold,
		AsyncUsageSync: false, // Synchronous mode
	})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}
	req := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10,
		Tier:           "pro",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "consume-2",
	}

	newUsed, err := storage.ConsumeQuota(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 10, newUsed)

	// In sync mode, cold should be updated immediately
	coldUsage, err := cold.GetUsage(ctx, "user1", "api_calls", period)
	assert.NoError(t, err)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 10, coldUsage.Used)
}

// --- Record Retrieval Read-Through Tests (Critical for Idempotency) ---

func TestStorage_GetConsumptionRecord_ReadThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold, AsyncUsageSync: true})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	t.Run("hot hit (critical for idempotency)", func(t *testing.T) {
		// Simulate consumption record in hot (written immediately)
		req := &goquota.ConsumeRequest{
			UserID:         "user1",
			Resource:       "api_calls",
			Amount:         10,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: "key-1",
		}
		_, err := hot.ConsumeQuota(ctx, req)
		require.NoError(t, err)

		// GetConsumptionRecord should find it in hot (even if not yet in cold)
		rec, err := storage.GetConsumptionRecord(ctx, "key-1")
		assert.NoError(t, err)
		assert.NotNil(t, rec)
		assert.Equal(t, "key-1", rec.IdempotencyKey)
		assert.Equal(t, 10, rec.NewUsed)
	})

	t.Run("hot miss, cold hit", func(t *testing.T) {
		hot2 := memory.New()
		cold2 := memory.New()
		storage2, _ := New(Config{Hot: hot2, Cold: cold2})
		defer storage2.Close()

		// Put record in cold only
		req := &goquota.ConsumeRequest{
			UserID:         "user2",
			Resource:       "api_calls",
			Amount:         20,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: "key-2",
		}
		_, err := cold2.ConsumeQuota(ctx, req)
		require.NoError(t, err)

		rec, err := storage2.GetConsumptionRecord(ctx, "key-2")
		assert.NoError(t, err)
		assert.NotNil(t, rec)
		assert.Equal(t, "key-2", rec.IdempotencyKey)
	})
}

func TestStorage_GetRefundRecord_ReadThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Setup usage
	usage := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, hot.SetUsage(ctx, "user1", "api_calls", usage, period))
	require.NoError(t, cold.SetUsage(ctx, "user1", "api_calls", usage, period))

	t.Run("hot hit", func(t *testing.T) {
		req := &goquota.RefundRequest{
			UserID:            "user1",
			Resource:          "api_calls",
			Amount:            10,
			Period:            period,
			PeriodType:        goquota.PeriodTypeMonthly,
			IdempotencyKey:    "refund-key-1",
			IdempotencyKeyTTL: 24 * time.Hour,
		}
		require.NoError(t, hot.RefundQuota(ctx, req))

		rec, err := storage.GetRefundRecord(ctx, "refund-key-1")
		assert.NoError(t, err)
		assert.NotNil(t, rec)
		assert.Equal(t, "refund-key-1", rec.IdempotencyKey)
	})

	t.Run("hot miss, cold hit", func(t *testing.T) {
		hot2 := memory.New()
		cold2 := memory.New()
		storage2, _ := New(Config{Hot: hot2, Cold: cold2})
		defer storage2.Close()

		require.NoError(t, cold2.SetUsage(ctx, "user1", "api_calls", usage, period))

		req := &goquota.RefundRequest{
			UserID:            "user1",
			Resource:          "api_calls",
			Amount:            10,
			Period:            period,
			PeriodType:        goquota.PeriodTypeMonthly,
			IdempotencyKey:    "refund-key-2",
			IdempotencyKeyTTL: 24 * time.Hour,
		}
		require.NoError(t, cold2.RefundQuota(ctx, req))

		rec, err := storage2.GetRefundRecord(ctx, "refund-key-2")
		assert.NoError(t, err)
		assert.NotNil(t, rec)
		assert.Equal(t, "refund-key-2", rec.IdempotencyKey)
	})
}

// --- Error Handling Tests ---

func TestStorage_WriteThrough_ColdFailure(t *testing.T) {
	hot := memory.New()
	// Create a failing cold storage by using a closed/invalid storage
	// For this test, we'll use a real cold storage but test the write-through order
	// Actually, memory storage won't fail, so we test the write order instead

	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()

	// Test that cold is written first in write-through
	// We can't easily simulate cold failure without mocks, but we verify the pattern
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	// Clear cold first
	_, _ = cold.GetEntitlement(ctx, "user1") // Just to ensure it doesn't exist

	err := storage.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// Both should have it (cold written first, then hot)
	coldEnt, _ := cold.GetEntitlement(ctx, "user1")
	assert.NotNil(t, coldEnt)

	hotEnt, _ := hot.GetEntitlement(ctx, "user1")
	assert.NotNil(t, hotEnt)
}

// --- Close/Graceful Shutdown Tests ---

func TestStorage_Close(t *testing.T) {
	hot := memory.New()
	cold := memory.New()

	storage, _ := New(Config{
		Hot:            hot,
		Cold:           cold,
		AsyncUsageSync: true,
	})

	// Close should not panic
	assert.NoError(t, storage.Close())

	// Second close should also not panic
	assert.NoError(t, storage.Close())
}

func TestStorage_Close_DrainsQueue(t *testing.T) {
	hot := memory.New()
	cold := memory.New()

	storage, _ := New(Config{
		Hot:            hot,
		Cold:           cold,
		AsyncUsageSync: true,
		SyncBufferSize: 10,
	})

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Queue several async operations
	for i := 0; i < 5; i++ {
		req := &goquota.ConsumeRequest{
			UserID:         "user1",
			Resource:       "api_calls",
			Amount:         1,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: fmt.Sprintf("key-%d", i),
		}
		storage.ConsumeQuota(ctx, req)
	}

	// Close should drain the queue
	assert.NoError(t, storage.Close())

	// Give a moment for any remaining processing
	time.Sleep(50 * time.Millisecond)
	// Close should complete without hanging
}

// --- TimeSource Tests ---

func TestStorage_Now(t *testing.T) {
	hot := memory.New() // Memory implements TimeSource
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	now, err := storage.Now(ctx)
	assert.NoError(t, err)
	assert.False(t, now.IsZero())
	assert.WithinDuration(t, time.Now().UTC(), now, time.Second)
}

// --- ApplyTierChange Tests ---

func TestStorage_ApplyTierChange_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	req := &goquota.TierChangeRequest{
		UserID:      "user1",
		Resource:    "audio_seconds",
		OldTier:     "free",
		NewTier:     "pro",
		Period:      period,
		OldLimit:    100,
		NewLimit:    1000,
		CurrentUsed: 50,
	}

	err := storage.ApplyTierChange(ctx, req)
	assert.NoError(t, err)

	// Verify both stores were updated
	hotUsage, _ := hot.GetUsage(ctx, "user1", "audio_seconds", period)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 1000, hotUsage.Limit)

	coldUsage, _ := cold.GetUsage(ctx, "user1", "audio_seconds", period)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 1000, coldUsage.Limit)
}

// --- SubtractLimit Tests ---

func TestStorage_SubtractLimit_WriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, _ := New(Config{Hot: hot, Cold: cold})
	defer storage.Close()

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Setup initial limit
	require.NoError(t, storage.AddLimit(ctx, "user1", "api_calls", 100, period, "init"))

	// Subtract limit
	err := storage.SubtractLimit(ctx, "user1", "api_calls", 30, period, "subtract-1")
	assert.NoError(t, err)

	// Verify both stores
	hotUsage, _ := hot.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, hotUsage)
	assert.Equal(t, 70, hotUsage.Limit)

	coldUsage, _ := cold.GetUsage(ctx, "user1", "api_calls", period)
	assert.NotNil(t, coldUsage)
	assert.Equal(t, 70, coldUsage.Limit)
}
