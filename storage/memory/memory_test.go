package memory

import (
	"context"
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
	if usage.Tier != "fluent" {
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
