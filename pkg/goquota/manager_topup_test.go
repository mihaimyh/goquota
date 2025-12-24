package goquota_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_TopUpLimit_Basic(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up credits
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Verify limit increased
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", usage.Limit)
	}
	if usage.Used != 0 {
		t.Errorf("Expected used 0, got %d", usage.Used)
	}
}

func TestManager_TopUpLimit_MultipleTopUps(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// First top-up
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 50, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("First TopUpLimit failed: %v", err)
	}

	// Second top-up
	err = manager.TopUpLimit(ctx, "user1", "api_calls", 75, goquota.WithTopUpIdempotencyKey("topup_2"))
	if err != nil {
		t.Fatalf("Second TopUpLimit failed: %v", err)
	}

	// Verify total limit
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 125 {
		t.Errorf("Expected limit 125 (50+75), got %d", usage.Limit)
	}
}

func TestManager_TopUpLimit_Idempotency(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	idempotencyKey := "topup_idempotent_1"

	// First top-up
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First TopUpLimit failed: %v", err)
	}

	// Second top-up with same idempotency key (should be ignored)
	err = manager.TopUpLimit(ctx, "user1", "api_calls", 200, goquota.WithTopUpIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second TopUpLimit should succeed (idempotent), got error: %v", err)
	}

	// Verify limit is still 100 (not 300)
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100 (idempotent), got %d", usage.Limit)
	}
}

func TestManager_TopUpLimit_ConcurrentTopUps(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	const numGoroutines = 10
	const topUpAmount = 10
	expectedTotal := numGoroutines * topUpAmount

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	// Launch concurrent top-ups
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idempotencyKey := "concurrent_topup_" + string(rune(id))
			err := manager.TopUpLimit(ctx, "user1", "api_calls", topUpAmount, goquota.WithTopUpIdempotencyKey(idempotencyKey))
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent top-up failed: %v", err)
	}

	// Verify total limit
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != expectedTotal {
		t.Errorf("Expected limit %d, got %d", expectedTotal, usage.Limit)
	}
}

func TestManager_TopUpLimit_InvalidAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Test zero amount
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 0)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount for zero amount, got %v", err)
	}

	// Test negative amount
	err = manager.TopUpLimit(ctx, "user1", "api_calls", -10)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount for negative amount, got %v", err)
	}
}

func TestManager_RefundCredits_Basic(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up first
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Refund credits
	err = manager.RefundCredits(ctx, "user1", "api_calls", 30, "test_refund", goquota.WithRefundIdempotencyKey("refund_1"))
	if err != nil {
		t.Fatalf("RefundCredits failed: %v", err)
	}

	// Verify limit decreased
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 70 {
		t.Errorf("Expected limit 70 (100-30), got %d", usage.Limit)
	}
}

func TestManager_RefundCredits_NegativeLimitPrevention(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up with small amount
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 50, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Try to refund more than available
	err = manager.RefundCredits(ctx, "user1", "api_calls", 100, "over_refund", goquota.WithRefundIdempotencyKey("refund_1"))
	if err != nil {
		t.Fatalf("RefundCredits should succeed (clamp to 0), got error: %v", err)
	}

	// Verify limit is clamped to 0
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 0 {
		t.Errorf("Expected limit 0 (clamped), got %d", usage.Limit)
	}
	if usage.Limit < 0 {
		t.Errorf("Limit should never be negative, got %d", usage.Limit)
	}
}

func TestManager_RefundCredits_Idempotency(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up first
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	idempotencyKey := "refund_idempotent_1"

	// First refund
	err = manager.RefundCredits(ctx, "user1", "api_calls", 30, "test", goquota.WithRefundIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First RefundCredits failed: %v", err)
	}

	// Second refund with same idempotency key (should be ignored)
	err = manager.RefundCredits(ctx, "user1", "api_calls", 50, "test", goquota.WithRefundIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second RefundCredits should succeed (idempotent), got error: %v", err)
	}

	// Verify limit is 70 (100-30), not 20 (100-30-50)
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 70 {
		t.Errorf("Expected limit 70 (idempotent), got %d", usage.Limit)
	}
}

func TestManager_RefundCredits_InvalidAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Test zero amount
	err := manager.RefundCredits(ctx, "user1", "api_calls", 0, "test")
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount for zero amount, got %v", err)
	}

	// Test negative amount
	err = manager.RefundCredits(ctx, "user1", "api_calls", -10, "test")
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount for negative amount, got %v", err)
	}
}

func TestManager_Consume_ForeverPeriod(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up credits
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Consume from forever period
	newUsed, err := manager.Consume(ctx, "user1", "api_calls", 30, goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if newUsed != 30 {
		t.Errorf("Expected newUsed 30, got %d", newUsed)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 30 {
		t.Errorf("Expected used 30, got %d", usage.Used)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", usage.Limit)
	}
	remaining := usage.Limit - usage.Used
	if remaining != 70 {
		t.Errorf("Expected remaining 70, got %d", remaining)
	}
}

func TestManager_Consume_ForeverPeriod_Exceeded(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Top up with small amount
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 50, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Try to consume more than available
	_, err = manager.Consume(ctx, "user1", "api_calls", 100, goquota.PeriodTypeForever)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}
}

func TestManager_Consume_AutoCascading(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"api_calls": 100, // Monthly limit
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeMonthly,
					goquota.PeriodTypeForever,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()

	// Set entitlement
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Top up forever credits
	err = manager.TopUpLimit(ctx, "user1", "api_calls", 50, goquota.WithTopUpIdempotencyKey("topup_1"))
	if err != nil {
		t.Fatalf("TopUpLimit failed: %v", err)
	}

	// Consume from monthly first (should use monthly quota)
	newUsed, err := manager.Consume(ctx, "user1", "api_calls", 80, goquota.PeriodTypeAuto)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}
	if newUsed != 80 {
		t.Errorf("Expected newUsed 80, got %d", newUsed)
	}

	// Verify monthly usage
	monthlyUsage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if monthlyUsage.Used != 80 {
		t.Errorf("Expected monthly used 80, got %d", monthlyUsage.Used)
	}

	// Try to consume more (should fallback to forever credits)
	newUsed, err = manager.Consume(ctx, "user1", "api_calls", 30, goquota.PeriodTypeAuto)
	if err != nil {
		t.Fatalf("Consume should fallback to forever, got error: %v", err)
	}
	if newUsed != 30 {
		t.Errorf("Expected newUsed 30, got %d", newUsed)
	}

	// Verify forever usage
	foreverUsage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if foreverUsage.Used != 30 {
		t.Errorf("Expected forever used 30, got %d", foreverUsage.Used)
	}
}

func TestManager_Consume_AutoCascading_AllExhausted(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeMonthly,
					goquota.PeriodTypeForever,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()

	// Set entitlement
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume all monthly quota
	_, err = manager.Consume(ctx, "user1", "api_calls", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Try to consume more with auto (should fail - no forever credits)
	_, err = manager.Consume(ctx, "user1", "api_calls", 50, goquota.PeriodTypeAuto)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded when all periods exhausted, got %v", err)
	}
}

func TestManager_InitialForeverCredits(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				InitialForeverCredits: map[string]int{
					"api_calls": 25, // Sign-up bonus
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()

	// Set entitlement (should trigger InitialForeverCredits)
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Verify initial credits were applied
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 25 {
		t.Errorf("Expected initial forever credits 25, got %d", usage.Limit)
	}
}

func TestManager_InitialForeverCredits_RaceCondition(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				InitialForeverCredits: map[string]int{
					"api_calls": 25,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()

	// Concurrent SetEntitlement calls (simulating race condition)
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := manager.SetEntitlement(ctx, &goquota.Entitlement{
				UserID:                "user1",
				Tier:                  "scholar",
				SubscriptionStartDate: time.Now().UTC(),
				UpdatedAt:             time.Now().UTC(),
			})
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent SetEntitlement failed: %v", err)
	}

	// Verify initial credits were applied exactly once (not 10 times)
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Limit != 25 {
		t.Errorf("Expected initial forever credits 25 (applied once), got %d", usage.Limit)
	}
}

func TestManager_GetQuota_ForeverPeriod_NoCredits(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Get quota for forever period with no credits
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage == nil {
		t.Fatal("GetQuota should return usage (even if zero)")
	}
	if usage.Limit != 0 {
		t.Errorf("Expected limit 0 for no credits, got %d", usage.Limit)
	}
	if usage.Used != 0 {
		t.Errorf("Expected used 0, got %d", usage.Used)
	}
}

func TestManager_TopUpAndConsume_Concurrent(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Initial top-up
	err := manager.TopUpLimit(ctx, "user1", "api_calls", 100, goquota.WithTopUpIdempotencyKey("initial"))
	if err != nil {
		t.Fatalf("Initial TopUpLimit failed: %v", err)
	}

	const numConsumers = 5
	const consumeAmount = 10
	var wg sync.WaitGroup
	errors := make(chan error, numConsumers)

	// Concurrent consumption
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := manager.Consume(ctx, "user1", "api_calls", consumeAmount, goquota.PeriodTypeForever, goquota.WithIdempotencyKey("consume_"+string(rune(id))))
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent consume failed: %v", err)
	}

	// Verify total consumed
	usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	expectedUsed := numConsumers * consumeAmount
	if usage.Used != expectedUsed {
		t.Errorf("Expected used %d, got %d", expectedUsed, usage.Used)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", usage.Limit)
	}
}

