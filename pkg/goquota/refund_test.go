package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_Refund(t *testing.T) {
	// Setup
	store := memory.New()
	cfg := goquota.Config{
		DefaultTier: "default",
		Tiers: map[string]goquota.TierConfig{
			"default": {
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
		},
		CacheTTL: time.Minute,
	}

	manager, err := goquota.NewManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()
	userID := "test_user_refund"
	resource := "api_calls"

	// 1. Initial Consumption
	_, err = manager.Consume(ctx, userID, resource, 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("failed to consume quota: %v", err)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.Used != 100 {
		t.Errorf("expected usage 100, got %d", usage.Used)
	}

	// 2. Refund Logic
	refundReq := &goquota.RefundRequest{
		UserID:         userID,
		Resource:       resource,
		Amount:         50,
		PeriodType:     goquota.PeriodTypeMonthly,
		Reason:         "service_failure",
		IdempotencyKey: "refund_123",
	}

	err = manager.Refund(ctx, refundReq)
	if err != nil {
		t.Errorf("failed to refund quota: %v", err)
	}

	// Verify usage after refund
	usage, err = manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.Used != 50 {
		t.Errorf("expected usage 50, got %d", usage.Used)
	}

	// 3. Idempotency Check
	// Try same refund again
	err = manager.Refund(ctx, refundReq)
	if err != nil {
		t.Errorf("failed to re-submit refund (idempotency check): %v", err)
	}

	// Usage should NOT change
	usage, err = manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.Used != 50 {
		t.Errorf("expected usage 50 (idempotent), got %d", usage.Used)
	}

	// 4. Over-refund (refund more than used)
	refundReq2 := &goquota.RefundRequest{
		UserID:         userID,
		Resource:       resource,
		Amount:         100, // Used is 50
		PeriodType:     goquota.PeriodTypeMonthly,
		Reason:         "correction",
		IdempotencyKey: "refund_124",
	}
	err = manager.Refund(ctx, refundReq2)
	if err != nil {
		t.Errorf("failed to process over-refund: %v", err)
	}

	// Usage should be 0, not negative
	usage, err = manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.Used != 0 {
		t.Errorf("expected usage 0, got %d", usage.Used)
	}
}

func TestManager_Refund_Concurrency(t *testing.T) {
	// Setup
	store := memory.New()
	cfg := goquota.Config{
		DefaultTier: "default",
		Tiers: map[string]goquota.TierConfig{
			"default": {
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
		},
		CacheTTL: time.Minute,
	}
	manager, _ := goquota.NewManager(store, cfg)
	ctx := context.Background()
	userID := "test_user_concurrent"
	resource := "api_calls"

	// Initial usage
	manager.Consume(ctx, userID, resource, 1000, goquota.PeriodTypeMonthly)

	// Run concurrent refunds with same idempotency key
	concurrency := 10
	errChan := make(chan error, concurrency)
	idempotencyKey := "concurrent_refund_key"

	for i := 0; i < concurrency; i++ {
		go func() {
			err := manager.Refund(ctx, &goquota.RefundRequest{
				UserID:         userID,
				Resource:       resource,
				Amount:         10,
				PeriodType:     goquota.PeriodTypeMonthly,
				IdempotencyKey: idempotencyKey,
			})
			errChan <- err
		}()
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("concurrent refund failed: %v", err)
		}
	}

	// Usage should decrease ONLY ONCE by 10
	usage, _ := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if usage.Used != 990 {
		t.Errorf("expected usage 990 (idempotent), got %d", usage.Used)
	}
}
