package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestStorage_AddLimit_Basic(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Add limit
	err := storage.AddLimit(ctx, "user1", "api_calls", 100, period, "topup_1")
	if err != nil {
		t.Fatalf("AddLimit failed: %v", err)
	}

	// Verify limit
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage == nil {
		t.Fatal("Usage should exist after AddLimit")
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", usage.Limit)
	}
}

func TestStorage_AddLimit_MultipleIncrements(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// First increment
	err := storage.AddLimit(ctx, "user1", "api_calls", 50, period, "topup_1")
	if err != nil {
		t.Fatalf("First AddLimit failed: %v", err)
	}

	// Second increment
	err = storage.AddLimit(ctx, "user1", "api_calls", 75, period, "topup_2")
	if err != nil {
		t.Fatalf("Second AddLimit failed: %v", err)
	}

	// Verify total
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 125 {
		t.Errorf("Expected limit 125 (50+75), got %d", usage.Limit)
	}
}

func TestStorage_AddLimit_Idempotency(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	idempotencyKey := "idempotent_1"

	// First call
	err := storage.AddLimit(ctx, "user1", "api_calls", 100, period, idempotencyKey)
	if err != nil {
		t.Fatalf("First AddLimit failed: %v", err)
	}

	// Second call with same idempotency key
	err = storage.AddLimit(ctx, "user1", "api_calls", 200, period, idempotencyKey)
	if err != goquota.ErrIdempotencyKeyExists {
		t.Errorf("Expected ErrIdempotencyKeyExists, got %v", err)
	}

	// Verify limit is still 100
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100 (idempotent), got %d", usage.Limit)
	}
}

func TestStorage_AddLimit_Concurrent(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	const numGoroutines = 10
	const incrementAmount = 10
	expectedTotal := numGoroutines * incrementAmount

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	// Concurrent increments
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idempotencyKey := "concurrent_" + string(rune(id))
			err := storage.AddLimit(ctx, "user1", "api_calls", incrementAmount, period, idempotencyKey)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent AddLimit failed: %v", err)
	}

	// Verify total
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != expectedTotal {
		t.Errorf("Expected limit %d, got %d", expectedTotal, usage.Limit)
	}
}

func TestStorage_SubtractLimit_Basic(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Add limit first
	err := storage.AddLimit(ctx, "user1", "api_calls", 100, period, "topup_1")
	if err != nil {
		t.Fatalf("AddLimit failed: %v", err)
	}

	// Subtract limit
	err = storage.SubtractLimit(ctx, "user1", "api_calls", 30, period, "refund_1")
	if err != nil {
		t.Fatalf("SubtractLimit failed: %v", err)
	}

	// Verify limit decreased
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 70 {
		t.Errorf("Expected limit 70 (100-30), got %d", usage.Limit)
	}
}

func TestStorage_SubtractLimit_NegativeClamp(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Add small limit
	err := storage.AddLimit(ctx, "user1", "api_calls", 50, period, "topup_1")
	if err != nil {
		t.Fatalf("AddLimit failed: %v", err)
	}

	// Try to subtract more than available
	err = storage.SubtractLimit(ctx, "user1", "api_calls", 100, period, "refund_1")
	if err != nil {
		t.Fatalf("SubtractLimit should succeed (clamp to 0), got error: %v", err)
	}

	// Verify limit is clamped to 0
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 0 {
		t.Errorf("Expected limit 0 (clamped), got %d", usage.Limit)
	}
	if usage.Limit < 0 {
		t.Errorf("Limit should never be negative, got %d", usage.Limit)
	}
}

func TestStorage_SubtractLimit_Idempotency(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
	}

	// Add limit first
	err := storage.AddLimit(ctx, "user1", "api_calls", 100, period, "topup_1")
	if err != nil {
		t.Fatalf("AddLimit failed: %v", err)
	}

	idempotencyKey := "refund_idempotent_1"

	// First subtract
	err = storage.SubtractLimit(ctx, "user1", "api_calls", 30, period, idempotencyKey)
	if err != nil {
		t.Fatalf("First SubtractLimit failed: %v", err)
	}

	// Second subtract with same idempotency key
	err = storage.SubtractLimit(ctx, "user1", "api_calls", 50, period, idempotencyKey)
	if err != goquota.ErrIdempotencyKeyExists {
		t.Errorf("Expected ErrIdempotencyKeyExists, got %v", err)
	}

	// Verify limit is 70 (100-30), not 20 (100-30-50)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 70 {
		t.Errorf("Expected limit 70 (idempotent), got %d", usage.Limit)
	}
}

func TestStorage_SubtractLimit_NoUsageRecord(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Subtract from non-existent usage (should not error, just do nothing)
	err := storage.SubtractLimit(ctx, "user1", "api_calls", 50, period, "refund_1")
	if err != nil {
		t.Fatalf("SubtractLimit should succeed (no-op), got error: %v", err)
	}
}

func TestStorage_AddLimit_PreservesUsed(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Create usage with some consumed
	usage := &goquota.Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     30,
		Limit:    100,
		Period:   period,
		Tier:     "default",
		UpdatedAt: time.Now().UTC(),
	}
	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	if err != nil {
		t.Fatalf("SetUsage failed: %v", err)
	}

	// Add limit
	err = storage.AddLimit(ctx, "user1", "api_calls", 50, period, "topup_1")
	if err != nil {
		t.Fatalf("AddLimit failed: %v", err)
	}

	// Verify limit increased but used preserved
	updatedUsage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if updatedUsage.Limit != 150 {
		t.Errorf("Expected limit 150 (100+50), got %d", updatedUsage.Limit)
	}
	if updatedUsage.Used != 30 {
		t.Errorf("Expected used 30 (preserved), got %d", updatedUsage.Used)
	}
}

func TestStorage_SubtractLimit_PreservesUsed(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	// Create usage with some consumed
	usage := &goquota.Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     30,
		Limit:    100,
		Period:   period,
		Tier:     "default",
		UpdatedAt: time.Now().UTC(),
	}
	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	if err != nil {
		t.Fatalf("SetUsage failed: %v", err)
	}

	// Subtract limit
	err = storage.SubtractLimit(ctx, "user1", "api_calls", 20, period, "refund_1")
	if err != nil {
		t.Fatalf("SubtractLimit failed: %v", err)
	}

	// Verify limit decreased but used preserved
	updatedUsage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if updatedUsage.Limit != 80 {
		t.Errorf("Expected limit 80 (100-20), got %d", updatedUsage.Limit)
	}
	if updatedUsage.Used != 30 {
		t.Errorf("Expected used 30 (preserved), got %d", updatedUsage.Used)
	}
}

