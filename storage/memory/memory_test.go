package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestStorage_GetSetEntitlement(t *testing.T) {
	storage := New()
	ctx := context.Background()

	// Test getting non-existent entitlement
	_, err := storage.GetEntitlement(ctx, "user1")
	if err != goquota.ErrEntitlementNotFound {
		t.Errorf("Expected ErrEntitlementNotFound, got %v", err)
	}

	// Test setting entitlement
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	err = storage.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Test getting entitlement
	retrieved, err := storage.GetEntitlement(ctx, "user1")
	if err != nil {
		t.Fatalf("GetEntitlement failed: %v", err)
	}

	if retrieved.UserID != ent.UserID {
		t.Errorf("UserID mismatch: got %s, want %s", retrieved.UserID, ent.UserID)
	}
	if retrieved.Tier != ent.Tier {
		t.Errorf("Tier mismatch: got %s, want %s", retrieved.Tier, ent.Tier)
	}
}

func TestStorage_GetUsage_NotFound(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	// Should return nil for non-existent usage
	if usage != nil {
		t.Errorf("Expected nil usage, got %+v", usage)
	}
}

func TestStorage_ConsumeQuota_Success(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume quota
	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   5,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	used, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}
	if used != 5 {
		t.Errorf("Expected 5 used returned, got %d", used)
	}

	// Verify usage
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Used != 5 {
		t.Errorf("Expected 5 used, got %d", usage.Used)
	}
	if usage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", usage.Limit)
	}
}

func TestStorage_ConsumeQuota_Exceeds(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Try to consume more than limit
	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   150,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	used, err := storage.ConsumeQuota(ctx, req)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}
	if used != 0 { // In memory implementation returns current usage on failure
		t.Errorf("Expected 0 used returned on failure, got %d", used)
	}
}

func TestStorage_ConsumeQuota_Multiple(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume multiple times
	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   10,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	for i := 0; i < 5; i++ {
		used, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Fatalf("ConsumeQuota iteration %d failed: %v", i, err)
		}
		if used != (i+1)*10 {
			t.Errorf("Iteration %d: expected %d used, got %d", i, (i+1)*10, used)
		}
	}

	// Verify total usage
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Used != 50 {
		t.Errorf("Expected 50 used (5 x 10), got %d", usage.Used)
	}
}

func TestStorage_ConsumeQuota_ZeroAmount(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   0,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Errorf("ConsumeQuota with 0 amount should succeed, got %v", err)
	}
}

func TestStorage_ConsumeQuota_NegativeAmount(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   -10,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, req)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount, got %v", err)
	}
}

func TestStorage_ConsumeQuota_WithIdempotencyKey_Success(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	idempotencyKey := "test-key-123"

	// First consumption with idempotency key
	req1 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: idempotencyKey,
	}

	used1, err := storage.ConsumeQuota(ctx, req1)
	if err != nil {
		t.Fatalf("First ConsumeQuota failed: %v", err)
	}
	if used1 != 5 {
		t.Errorf("Expected 5 used returned, got %d", used1)
	}

	// Second consumption with same idempotency key - should return cached result
	req2 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: idempotencyKey,
	}

	used2, err := storage.ConsumeQuota(ctx, req2)
	if err != nil {
		t.Fatalf("Second ConsumeQuota failed: %v", err)
	}
	if used2 != 5 {
		t.Errorf("Expected cached 5 used returned, got %d", used2)
	}

	// Verify usage was only consumed once
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 5 {
		t.Errorf("Expected usage 5 (consumed once), got %d", usage.Used)
	}

	// Verify consumption record exists
	record, err := storage.GetConsumptionRecord(ctx, idempotencyKey)
	if err != nil {
		t.Fatalf("GetConsumptionRecord failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected consumption record, got nil")
	}
	if record.NewUsed != 5 {
		t.Errorf("Expected NewUsed 5, got %d", record.NewUsed)
	}
	if record.Amount != 5 {
		t.Errorf("Expected Amount 5, got %d", record.Amount)
	}
}

func TestStorage_ConsumeQuota_WithIdempotencyKey_DifferentKeys(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// First consumption with idempotency key 1
	req1 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "key-1",
	}

	_, err := storage.ConsumeQuota(ctx, req1)
	if err != nil {
		t.Fatalf("First ConsumeQuota failed: %v", err)
	}

	// Second consumption with different idempotency key - should consume again
	req2 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "key-2",
	}

	_, err = storage.ConsumeQuota(ctx, req2)
	if err != nil {
		t.Fatalf("Second ConsumeQuota failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected usage 10 (consumed twice), got %d", usage.Used)
	}
}

func TestStorage_GetConsumptionRecord_NotFound(t *testing.T) {
	storage := New()
	ctx := context.Background()

	record, err := storage.GetConsumptionRecord(ctx, "non-existent-key")
	if err != nil {
		t.Fatalf("GetConsumptionRecord failed: %v", err)
	}
	if record != nil {
		t.Errorf("Expected nil record, got %+v", record)
	}
}

func TestStorage_ConsumeQuota_WithIdempotencyKey_DifferentAmount(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	idempotencyKey := "test-key-diff-amount"

	// First consumption with idempotency key
	req1 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: idempotencyKey,
	}

	used1, err := storage.ConsumeQuota(ctx, req1)
	if err != nil {
		t.Fatalf("First ConsumeQuota failed: %v", err)
	}

	// Second consumption with same idempotency key but different amount - should return cached result
	req2 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10, // Different amount
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: idempotencyKey,
	}

	used2, err := storage.ConsumeQuota(ctx, req2)
	if err != nil {
		t.Fatalf("Second ConsumeQuota failed: %v", err)
	}
	// Should return cached result
	if used2 != used1 {
		t.Errorf("Expected cached result %d, got %d", used1, used2)
	}

	// Verify usage was only consumed once (5, not 10)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 5 {
		t.Errorf("Expected usage 5 (consumed once), got %d", usage.Used)
	}
}

func TestStorage_ConsumeQuota_WithIdempotencyKey_EmptyString(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume with empty string idempotency key - should work normally
	req1 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "", // Empty string
	}

	_, err := storage.ConsumeQuota(ctx, req1)
	if err != nil {
		t.Fatalf("ConsumeQuota with empty idempotency key failed: %v", err)
	}

	// Consume again with empty string - should consume again
	req2 := &goquota.ConsumeRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         5,
		Tier:           "scholar",
		Period:         period,
		Limit:          100,
		IdempotencyKey: "",
	}

	_, err = storage.ConsumeQuota(ctx, req2)
	if err != nil {
		t.Fatalf("Second ConsumeQuota failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected usage 10 (consumed twice), got %d", usage.Used)
	}
}

func TestStorage_ApplyTierChange(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume some quota first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "audio_seconds",
		Amount:   1000,
		Tier:     "scholar",
		Period:   period,
		Limit:    3600,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Apply tier change
	tierReq := &goquota.TierChangeRequest{
		UserID:      "user1",
		Resource:    "audio_seconds",
		OldTier:     "scholar",
		NewTier:     "fluent",
		Period:      period,
		OldLimit:    3600,
		NewLimit:    10000,
		CurrentUsed: 100,
	}

	err = storage.ApplyTierChange(ctx, tierReq)
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Verify new limit
	usage, err := storage.GetUsage(ctx, "user1", "audio_seconds", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Limit != 10000 {
		t.Errorf("Expected new limit 10000, got %d", usage.Limit)
	}
	const expectedTierFluent = "fluent"
	if usage.Tier != expectedTierFluent {
		t.Errorf("Expected tier fluent, got %s", usage.Tier)
	}
}

func TestStorage_Clear(t *testing.T) {
	storage := New()
	ctx := context.Background()

	// Add some data
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	_ = storage.SetEntitlement(ctx, ent)

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	_ = storage.SetUsage(ctx, "user1", "api_calls", &goquota.Usage{
		Used:  10,
		Limit: 100,
	}, period)

	// Clear storage
	err := storage.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify data is gone
	_, err = storage.GetEntitlement(ctx, "user1")
	if err != goquota.ErrEntitlementNotFound {
		t.Errorf("Expected ErrEntitlementNotFound after Clear, got %v", err)
	}

	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage != nil {
		t.Errorf("Expected nil usage after Clear, got %+v", usage)
	}
}

// Phase 1.1: Memory Storage RefundQuota Tests

func TestStorage_RefundQuota_Basic(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// First consume some quota
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Refund some quota
	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     20,
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
		Reason:     "service_failure",
	}

	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}

	// Verify usage decreased
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 30 {
		t.Errorf("Expected 30 used (50 - 20), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_OverRefund(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume some quota
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Refund more than used (should clamp to 0)
	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     100, // More than used (50)
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
		Reason:     "correction",
	}

	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}

	// Verify usage is 0, not negative
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 0 {
		t.Errorf("Expected 0 used (clamped), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_NoUsage(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Refund when no usage exists (should succeed)
	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     20,
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
		Reason:     "no_usage",
	}

	err := storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota with no usage should succeed, got %v", err)
	}

	// Verify no usage was created
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage != nil {
		t.Errorf("Expected nil usage, got %+v", usage)
	}
}

func TestStorage_RefundQuota_Idempotency(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume some quota
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	idempotencyKey := "refund-key-123"

	// First refund
	refundReq1 := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: idempotencyKey,
		Reason:         "test",
	}

	err = storage.RefundQuota(ctx, refundReq1)
	if err != nil {
		t.Fatalf("First RefundQuota failed: %v", err)
	}

	// Second refund with same idempotency key (should be idempotent)
	refundReq2 := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: idempotencyKey,
		Reason:         "test",
	}

	err = storage.RefundQuota(ctx, refundReq2)
	if err != nil {
		t.Fatalf("Second RefundQuota (idempotent) failed: %v", err)
	}

	// Verify usage only decreased once (50 - 20 = 30)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 30 {
		t.Errorf("Expected 30 used (refunded once), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_Concurrent(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume quota first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   1000,
		Tier:     "scholar",
		Period:   period,
		Limit:    2000,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Concurrent refunds
	const goroutines = 100
	errChan := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(_ int) {
			refundReq := &goquota.RefundRequest{
				UserID:         "user1",
				Resource:       "api_calls",
				Amount:         1,
				PeriodType:     goquota.PeriodTypeDaily,
				Period:         period,
				IdempotencyKey: "", // No idempotency key - each should refund
			}
			errChan <- storage.RefundQuota(ctx, refundReq)
		}(i)
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent refund %d failed: %v", i, err)
		}
	}

	// Verify final usage (1000 - 100 = 900)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 900 {
		t.Errorf("Expected 900 used (1000 - 100), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_ConcurrentWithConsume(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Initial consumption
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   500,
		Tier:     "scholar",
		Period:   period,
		Limit:    2000,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("Initial ConsumeQuota failed: %v", err)
	}

	const consumeGoroutines = 100
	const refundGoroutines = 50
	errChan := make(chan error, consumeGoroutines+refundGoroutines)

	// Concurrent consumes
	for i := 0; i < consumeGoroutines; i++ {
		go func(_ int) {
			req := &goquota.ConsumeRequest{
				UserID:   "user1",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    2000,
			}
			_, err := storage.ConsumeQuota(ctx, req)
			errChan <- err
		}(i)
	}

	// Concurrent refunds
	for i := 0; i < refundGoroutines; i++ {
		go func(_ int) {
			refundReq := &goquota.RefundRequest{
				UserID:     "user1",
				Resource:   "api_calls",
				Amount:     1,
				PeriodType: goquota.PeriodTypeDaily,
				Period:     period,
			}
			errChan <- storage.RefundQuota(ctx, refundReq)
		}(i)
	}

	// Collect errors
	for i := 0; i < consumeGoroutines+refundGoroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent operation %d failed: %v", i, err)
		}
	}

	// Verify final usage (500 + 100 - 50 = 550)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 550 {
		t.Errorf("Expected 550 used (500 + 100 - 50), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_PeriodCalculation(t *testing.T) {
	storage := New()
	ctx := context.Background()

	// Test period calculation fallback when Period is not set
	now := time.Now().UTC()
	expectedStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	expectedEnd := expectedStart.AddDate(0, 1, 0)

	// Consume with explicit period
	period := goquota.Period{
		Start: expectedStart,
		End:   expectedEnd,
		Type:  goquota.PeriodTypeMonthly,
	}

	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Refund without Period set (should use PeriodType to calculate)
	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     20,
		PeriodType: goquota.PeriodTypeMonthly,
		// Period not set - should be calculated
		Reason: "test",
	}

	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota with period calculation failed: %v", err)
	}

	// Verify refund worked
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 30 {
		t.Errorf("Expected 30 used (50 - 20), got %d", usage.Used)
	}
}

func TestStorage_RefundQuota_InvalidPeriod(t *testing.T) {
	storage := New()
	ctx := context.Background()

	// Refund with invalid period type
	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     20,
		PeriodType: goquota.PeriodType("invalid"),
		// Period not set
		Reason: "test",
	}

	err := storage.RefundQuota(ctx, refundReq)
	if err != goquota.ErrInvalidPeriod {
		t.Errorf("Expected ErrInvalidPeriod, got %v", err)
	}
}

func TestStorage_RefundQuota_NegativeAmount(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     -10,
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
	}

	err := storage.RefundQuota(ctx, refundReq)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount, got %v", err)
	}
}

func TestStorage_RefundQuota_ZeroAmount(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	refundReq := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     0,
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
	}

	err := storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Errorf("RefundQuota with 0 amount should succeed (no-op), got %v", err)
	}
}

// Phase 1.2: Memory Storage GetRefundRecord Tests

func TestStorage_GetRefundRecord_Found(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Consume and refund with idempotency key
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	idempotencyKey := "refund-record-key"
	refundReq := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: idempotencyKey,
		Reason:         "test_reason",
		Metadata:       map[string]string{"key": "value"},
	}

	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}

	// Retrieve refund record
	record, err := storage.GetRefundRecord(ctx, idempotencyKey)
	if err != nil {
		t.Fatalf("GetRefundRecord failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected refund record, got nil")
	}
	if record.RefundID != idempotencyKey {
		t.Errorf("Expected RefundID %s, got %s", idempotencyKey, record.RefundID)
	}
	if record.UserID != "user1" {
		t.Errorf("Expected UserID user1, got %s", record.UserID)
	}
	if record.Resource != "api_calls" {
		t.Errorf("Expected Resource api_calls, got %s", record.Resource)
	}
	if record.Amount != 20 {
		t.Errorf("Expected Amount 20, got %d", record.Amount)
	}
	if record.Reason != "test_reason" {
		t.Errorf("Expected Reason test_reason, got %s", record.Reason)
	}
	if record.Metadata["key"] != "value" {
		t.Errorf("Expected Metadata[key]=value, got %v", record.Metadata)
	}
}

func TestStorage_GetRefundRecord_NotFound(t *testing.T) {
	storage := New()
	ctx := context.Background()

	record, err := storage.GetRefundRecord(ctx, "non-existent-key")
	if err != nil {
		t.Fatalf("GetRefundRecord failed: %v", err)
	}
	if record != nil {
		t.Errorf("Expected nil record, got %+v", record)
	}
}

func TestStorage_GetRefundRecord_Concurrent(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Create a refund record
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	idempotencyKey := "concurrent-read-key"
	refundReq := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: idempotencyKey,
	}

	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}

	// Concurrent reads
	const goroutines = 50
	recordChan := make(chan *goquota.RefundRecord, goroutines)
	errChan := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			record, err := storage.GetRefundRecord(ctx, idempotencyKey)
			recordChan <- record
			errChan <- err
		}()
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent GetRefundRecord %d failed: %v", i, err)
		}
		record := <-recordChan
		if record == nil {
			t.Errorf("Concurrent GetRefundRecord %d returned nil", i)
		} else if record.RefundID != idempotencyKey {
			t.Errorf("Concurrent GetRefundRecord %d: expected RefundID %s, got %s", i, idempotencyKey, record.RefundID)
		}
	}
}

// Phase 1.3: Memory Storage SetUsage Tests

func TestStorage_SetUsage_Basic(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	usage := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      75,
		Limit:     100,
		Period:    period,
		Tier:      "scholar",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	if err != nil {
		t.Fatalf("SetUsage failed: %v", err)
	}

	// Verify usage was set
	retrieved, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected usage, got nil")
	}
	if retrieved.Used != 75 {
		t.Errorf("Expected Used 75, got %d", retrieved.Used)
	}
	if retrieved.Limit != 100 {
		t.Errorf("Expected Limit 100, got %d", retrieved.Limit)
	}
	if retrieved.Tier != "scholar" {
		t.Errorf("Expected Tier scholar, got %s", retrieved.Tier)
	}
}

func TestStorage_SetUsage_Overwrite(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Set initial usage
	usage1 := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "scholar",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage1, period)
	if err != nil {
		t.Fatalf("First SetUsage failed: %v", err)
	}

	// Overwrite with new usage
	usage2 := &goquota.Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      80,
		Limit:     150,
		Period:    period,
		Tier:      "fluent",
		UpdatedAt: time.Now().UTC(),
	}

	err = storage.SetUsage(ctx, "user1", "api_calls", usage2, period)
	if err != nil {
		t.Fatalf("Second SetUsage failed: %v", err)
	}

	// Verify overwritten usage
	retrieved, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if retrieved.Used != 80 {
		t.Errorf("Expected Used 80 (overwritten), got %d", retrieved.Used)
	}
	if retrieved.Limit != 150 {
		t.Errorf("Expected Limit 150 (overwritten), got %d", retrieved.Limit)
	}
	if retrieved.Tier != "fluent" {
		t.Errorf("Expected Tier fluent (overwritten), got %s", retrieved.Tier)
	}
}

func TestStorage_SetUsage_NilUsage(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", nil, period)
	if err == nil {
		t.Error("Expected error for nil usage, got nil")
	}
}

func TestStorage_SetUsage_Concurrent(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			usage := &goquota.Usage{
				UserID:    "user1",
				Resource:  "api_calls",
				Used:      id,
				Limit:     100,
				Period:    period,
				Tier:      "scholar",
				UpdatedAt: time.Now().UTC(),
			}
			errChan <- storage.SetUsage(ctx, "user1", "api_calls", usage, period)
		}(i)
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent SetUsage %d failed: %v", i, err)
		}
	}

	// Verify final usage (should be one of the concurrent writes)
	retrieved, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected usage after concurrent SetUsage, got nil")
	}
	// The final value should be one of the concurrent writes (0-49)
	if retrieved.Used < 0 || retrieved.Used >= goroutines {
		t.Errorf("Expected Used in range [0, %d), got %d", goroutines, retrieved.Used)
	}
}

// Phase 3.1: Memory Storage Concurrency Tests

func TestStorage_ConcurrentConsumeRefund(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Initial consumption
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   500,
		Tier:     "scholar",
		Period:   period,
		Limit:    2000,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("Initial ConsumeQuota failed: %v", err)
	}

	const consumeGoroutines = 100
	const refundGoroutines = 50
	errChan := make(chan error, consumeGoroutines+refundGoroutines)

	// Concurrent consumes
	for i := 0; i < consumeGoroutines; i++ {
		go func() {
			req := &goquota.ConsumeRequest{
				UserID:   "user1",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    2000,
			}
			_, err := storage.ConsumeQuota(ctx, req)
			errChan <- err
		}()
	}

	// Concurrent refunds
	for i := 0; i < refundGoroutines; i++ {
		go func() {
			refundReq := &goquota.RefundRequest{
				UserID:     "user1",
				Resource:   "api_calls",
				Amount:     1,
				PeriodType: goquota.PeriodTypeDaily,
				Period:     period,
			}
			errChan <- storage.RefundQuota(ctx, refundReq)
		}()
	}

	// Collect errors
	for i := 0; i < consumeGoroutines+refundGoroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent operation %d failed: %v", i, err)
		}
	}

	// Verify final usage (500 + 100 - 50 = 550)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 550 {
		t.Errorf("Expected 550 used (500 + 100 - 50), got %d", usage.Used)
	}
}

func TestStorage_ConcurrentTierChange(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume some quota first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "audio_seconds",
		Amount:   1000,
		Tier:     "scholar",
		Period:   period,
		Limit:    3600,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)

	// Concurrent tier changes
	for i := 0; i < goroutines; i++ {
		go func(_ int) {
			tierReq := &goquota.TierChangeRequest{
				UserID:      "user1",
				Resource:    "audio_seconds",
				OldTier:     "scholar",
				NewTier:     "fluent",
				Period:      period,
				OldLimit:    3600,
				NewLimit:    18000,
				CurrentUsed: 1000,
			}
			errChan <- storage.ApplyTierChange(ctx, tierReq)
		}(i)
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent tier change %d failed: %v", i, err)
		}
	}

	// Verify tier was changed (last write wins)
	usage, err := storage.GetUsage(ctx, "user1", "audio_seconds", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	const expectedTierFluent = "fluent"
	if usage.Tier != expectedTierFluent {
		t.Errorf("Expected tier fluent, got %s", usage.Tier)
	}
	if usage.Limit != 18000 {
		t.Errorf("Expected limit 18000, got %d", usage.Limit)
	}
}

func TestStorage_ConcurrentReadWrite(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Initial consumption
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   100,
		Tier:     "scholar",
		Period:   period,
		Limit:    1000,
	}

	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("Initial ConsumeQuota failed: %v", err)
	}

	const readers = 100
	const writers = 50
	readErrChan := make(chan error, readers)
	writeErrChan := make(chan error, writers)

	// Concurrent readers
	for i := 0; i < readers; i++ {
		go func() {
			_, err := storage.GetUsage(ctx, "user1", "api_calls", period)
			readErrChan <- err
		}()
	}

	// Concurrent writers
	for i := 0; i < writers; i++ {
		go func(_ int) {
			req := &goquota.ConsumeRequest{
				UserID:   "user1",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    1000,
			}
			_, err := storage.ConsumeQuota(ctx, req)
			writeErrChan <- err
		}(i)
	}

	// Collect errors
	for i := 0; i < readers; i++ {
		if err := <-readErrChan; err != nil {
			t.Errorf("Concurrent read %d failed: %v", i, err)
		}
	}

	for i := 0; i < writers; i++ {
		if err := <-writeErrChan; err != nil {
			t.Errorf("Concurrent write %d failed: %v", i, err)
		}
	}

	// Verify final usage (100 + 50 = 150)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 150 {
		t.Errorf("Expected 150 used (100 + 50), got %d", usage.Used)
	}
}

func TestStorage_ConcurrentIdempotencyKeys(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)

	// Concurrent consumes with different idempotency keys
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			req := &goquota.ConsumeRequest{
				UserID:         "user1",
				Resource:       "api_calls",
				Amount:         1,
				Tier:           "scholar",
				Period:         period,
				Limit:          1000,
				IdempotencyKey: fmt.Sprintf("key-%d", id),
			}
			_, err := storage.ConsumeQuota(ctx, req)
			errChan <- err
		}(i)
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent consume %d failed: %v", i, err)
		}
	}

	// Verify all consumes were applied (50 total)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != goroutines {
		t.Errorf("Expected %d used, got %d", goroutines, usage.Used)
	}
}

func TestStorage_ConcurrentSameIdempotencyKey(t *testing.T) {
	storage := New()
	ctx := context.Background()

	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	const goroutines = 50
	idempotencyKey := "same-key-123"
	errChan := make(chan error, goroutines)

	// Concurrent consumes with same idempotency key
	for i := 0; i < goroutines; i++ {
		go func() {
			req := &goquota.ConsumeRequest{
				UserID:         "user1",
				Resource:       "api_calls",
				Amount:         1,
				Tier:           "scholar",
				Period:         period,
				Limit:          1000,
				IdempotencyKey: idempotencyKey,
			}
			_, err := storage.ConsumeQuota(ctx, req)
			errChan <- err
		}()
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent consume %d failed: %v", i, err)
		}
	}

	// Verify only one consume was applied (idempotent)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 1 {
		t.Errorf("Expected 1 used (idempotent), got %d", usage.Used)
	}
}
