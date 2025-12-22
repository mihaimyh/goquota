// +build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// getTestConnectionString returns a connection string for testing
// Uses POSTGRES_TEST_DSN environment variable or defaults to localhost
func getTestConnectionString() string {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/goquota_test?sslmode=disable"
	}
	return dsn
}

// setupTestStorage creates a test storage instance
func setupTestStorage(t *testing.T) *Storage {
	ctx := context.Background()
	config := DefaultConfig()
	config.ConnectionString = getTestConnectionString()
	config.CleanupEnabled = false // Disable cleanup in tests
	
	storage, err := New(ctx, config)
	if err != nil {
		t.Skipf("Skipping test: failed to connect to PostgreSQL: %v", err)
	}
	
	// Clean up test data
	_, _ = storage.pool.Exec(ctx, "TRUNCATE TABLE entitlements, quota_usage, consumption_records, refund_records CASCADE")
	
	return storage
}

func TestStorage_GetSetEntitlement(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	// Test getting non-existent entitlement
	_, err := storage.GetEntitlement(ctx, "user1")
	if err != goquota.ErrEntitlementNotFound {
		t.Errorf("Expected ErrEntitlementNotFound, got %v", err)
	}
	
	// Test setting entitlement
	now := time.Now().UTC()
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: now,
		UpdatedAt:             now,
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
	storage := setupTestStorage(t)
	defer storage.Close()
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
	storage := setupTestStorage(t)
	defer storage.Close()
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
	storage := setupTestStorage(t)
	defer storage.Close()
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
	if used != 0 {
		t.Errorf("Expected 0 used returned on failure, got %d", used)
	}
}

func TestStorage_ConsumeQuota_Idempotency(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	req := &goquota.ConsumeRequest{
		UserID:          "user1",
		Resource:        "api_calls",
		Amount:          10,
		Tier:            "scholar",
		Period:          period,
		Limit:           100,
		IdempotencyKey:  "test-key-123",
		IdempotencyKeyTTL: 24 * time.Hour,
	}
	
	// First consumption
	used1, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("First ConsumeQuota failed: %v", err)
	}
	
	// Second consumption with same idempotency key (should return cached result)
	used2, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("Second ConsumeQuota failed: %v", err)
	}
	
	if used1 != used2 {
		t.Errorf("Idempotency failed: first=%d, second=%d", used1, used2)
	}
	
	// Verify usage was only incremented once
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected 10 used (consumed once), got %d", usage.Used)
	}
}

func TestStorage_ConsumeQuota_ScopedIdempotency(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// User1 consumes with key "key-123"
	req1 := &goquota.ConsumeRequest{
		UserID:          "user1",
		Resource:        "api_calls",
		Amount:          10,
		Tier:            "scholar",
		Period:          period,
		Limit:           100,
		IdempotencyKey:  "key-123",
		IdempotencyKeyTTL: 24 * time.Hour,
	}
	
	// User2 consumes with same key "key-123" (should work - scoped to user)
	req2 := &goquota.ConsumeRequest{
		UserID:          "user2",
		Resource:        "api_calls",
		Amount:          10,
		Tier:            "scholar",
		Period:          period,
		Limit:           100,
		IdempotencyKey:  "key-123", // Same key, different user
		IdempotencyKeyTTL: 24 * time.Hour,
	}
	
	used1, err := storage.ConsumeQuota(ctx, req1)
	if err != nil {
		t.Fatalf("User1 ConsumeQuota failed: %v", err)
	}
	
	used2, err := storage.ConsumeQuota(ctx, req2)
	if err != nil {
		t.Fatalf("User2 ConsumeQuota failed: %v", err)
	}
	
	// Both should succeed (different users, same key is allowed)
	if used1 != 10 || used2 != 10 {
		t.Errorf("Scoped idempotency failed: user1=%d, user2=%d", used1, used2)
	}
}

func TestStorage_ConsumeQuota_Concurrent(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Test concurrent consumption (tests UPSERT pattern and SELECT FOR UPDATE)
	const numGoroutines = 10
	const amountPerRequest = 1
	const limit = 100
	
	results := make(chan int, numGoroutines)
	errors := make(chan error, numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			req := &goquota.ConsumeRequest{
				UserID:   "user1",
				Resource: "api_calls",
				Amount:   amountPerRequest,
				Tier:     "scholar",
				Period:   period,
				Limit:    limit,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				errors <- err
				return
			}
			results <- used
		}(i)
	}
	
	// Collect results
	var totalUsed int
	var errCount int
	for i := 0; i < numGoroutines; i++ {
		select {
		case used := <-results:
			totalUsed = used // Last value (should be cumulative)
		case err := <-errors:
			if err != goquota.ErrQuotaExceeded {
				t.Errorf("Unexpected error: %v", err)
			}
			errCount++
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for results")
		}
	}
	
	// Verify final usage
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	
	expectedUsed := numGoroutines * amountPerRequest
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (concurrent), got %d", expectedUsed, usage.Used)
	}
	if totalUsed != expectedUsed {
		t.Errorf("Expected total used %d, got %d", expectedUsed, totalUsed)
	}
}

func TestStorage_RefundQuota(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
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
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: "refund-1",
		IdempotencyKeyTTL: 24 * time.Hour,
		Reason:        "Test refund",
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
	
	expectedUsed := 50 - 20
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used after refund, got %d", expectedUsed, usage.Used)
	}
}

func TestStorage_RefundQuota_Idempotency(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Consume first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	_, _ = storage.ConsumeQuota(ctx, consumeReq)
	
	// Refund with idempotency key
	refundReq := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: "refund-key-123",
		IdempotencyKeyTTL: 24 * time.Hour,
	}
	
	// First refund
	err := storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("First RefundQuota failed: %v", err)
	}
	
	// Second refund with same key (should be idempotent)
	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("Second RefundQuota failed: %v", err)
	}
	
	// Verify usage was only decremented once
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	expectedUsed := 50 - 10 // Only refunded once
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (refunded once), got %d", expectedUsed, usage.Used)
	}
}

func TestStorage_ApplyTierChange(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Create initial usage
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	_, _ = storage.ConsumeQuota(ctx, consumeReq)
	
	// Apply tier change
	tierChangeReq := &goquota.TierChangeRequest{
		UserID:      "user1",
		Resource:    "api_calls",
		OldTier:     "scholar",
		NewTier:     "premium",
		Period:      period,
		OldLimit:    100,
		NewLimit:    200,
		CurrentUsed: 50,
	}
	
	err := storage.ApplyTierChange(ctx, tierChangeReq)
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}
	
	// Verify limit updated
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Limit != 200 {
		t.Errorf("Expected limit 200, got %d", usage.Limit)
	}
	if usage.Tier != "premium" {
		t.Errorf("Expected tier premium, got %s", usage.Tier)
	}
}

func TestStorage_GetConsumptionRecord(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Consume with idempotency key
	req := &goquota.ConsumeRequest{
		UserID:          "user1",
		Resource:        "api_calls",
		Amount:          10,
		Tier:            "scholar",
		Period:          period,
		Limit:           100,
		IdempotencyKey:  "test-consumption-key",
		IdempotencyKeyTTL: 24 * time.Hour,
	}
	
	_, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}
	
	// Get consumption record
	record, err := storage.GetConsumptionRecord(ctx, "test-consumption-key")
	if err != nil {
		t.Fatalf("GetConsumptionRecord failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected consumption record, got nil")
	}
	if record.Amount != 10 {
		t.Errorf("Expected amount 10, got %d", record.Amount)
	}
	if record.NewUsed != 10 {
		t.Errorf("Expected newUsed 10, got %d", record.NewUsed)
	}
}

func TestStorage_GetRefundRecord(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Consume first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	_, _ = storage.ConsumeQuota(ctx, consumeReq)
	
	// Refund with idempotency key
	refundReq := &goquota.RefundRequest{
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10,
		PeriodType:     goquota.PeriodTypeDaily,
		Period:         period,
		IdempotencyKey: "test-refund-key",
		IdempotencyKeyTTL: 24 * time.Hour,
		Reason:         "Test refund",
	}
	
	err := storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}
	
	// Get refund record
	record, err := storage.GetRefundRecord(ctx, "test-refund-key")
	if err != nil {
		t.Fatalf("GetRefundRecord failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected refund record, got nil")
	}
	if record.Amount != 10 {
		t.Errorf("Expected amount 10, got %d", record.Amount)
	}
	if record.Reason != "Test refund" {
		t.Errorf("Expected reason 'Test refund', got %s", record.Reason)
	}
}

func TestStorage_RateLimiting_DelegatedToMemory(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	// Rate limiting should work via embedded memory.Storage
	req := &goquota.RateLimitRequest{
		UserID:    "user1",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
		Now:       time.Now().UTC(),
	}
	
	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	if err != nil {
		t.Fatalf("CheckRateLimit failed: %v", err)
	}
	if !allowed {
		t.Error("Expected request to be allowed")
	}
	if remaining < 0 {
		t.Errorf("Expected non-negative remaining, got %d", remaining)
	}
	if resetTime.IsZero() {
		t.Error("Expected non-zero reset time")
	}
}

func TestStorage_Cleanup(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Create consumption record with short TTL
	req := &goquota.ConsumeRequest{
		UserID:          "user1",
		Resource:        "api_calls",
		Amount:          10,
		Tier:            "scholar",
		Period:          period,
		Limit:           100,
		IdempotencyKey:  "cleanup-test-key",
		IdempotencyKeyTTL: 1 * time.Second, // Very short TTL
	}
	
	_, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}
	
	// Verify record exists
	record, err := storage.GetConsumptionRecord(ctx, "cleanup-test-key")
	if err != nil {
		t.Fatalf("GetConsumptionRecord failed: %v", err)
	}
	if record == nil {
		t.Fatal("Expected consumption record to exist")
	}
	
	// Wait for expiration
	time.Sleep(2 * time.Second)
	
	// Run cleanup
	err = storage.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	
	// Record should be deleted (but we can't easily verify without direct DB query)
	// The cleanup should complete without error
}

func TestStorage_Close(t *testing.T) {
	storage := setupTestStorage(t)
	
	// Close should not panic
	storage.Close()
	
	// Second close should be safe
	storage.Close()
}

func TestStorage_New_EmptyConnectionString(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	config.ConnectionString = "" // Empty connection string
	
	_, err := New(ctx, config)
	if err == nil {
		t.Error("Expected error for empty connection string")
	}
}

func TestStorage_New_InvalidConnectionString(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	config.ConnectionString = "invalid://connection:string"
	
	_, err := New(ctx, config)
	if err == nil {
		t.Error("Expected error for invalid connection string")
	}
}

func TestStorage_New_WithCustomConfig(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	config.ConnectionString = getTestConnectionString()
	config.MaxConns = 20
	config.MinConns = 5
	config.MaxConnLifetime = 2 * time.Hour
	config.MaxConnIdleTime = 1 * time.Hour
	config.CleanupEnabled = false
	
	storage, err := New(ctx, config)
	if err != nil {
		t.Skipf("Skipping test: failed to connect to PostgreSQL: %v", err)
	}
	defer storage.Close()
	
	// Verify it works
	err = storage.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestStorage_Ping(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	err := storage.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestStorage_DefaultConfig(t *testing.T) {
	config := DefaultConfig()
	
	if config.MaxConns != 10 {
		t.Errorf("Expected MaxConns 10, got %d", config.MaxConns)
	}
	if config.MinConns != 2 {
		t.Errorf("Expected MinConns 2, got %d", config.MinConns)
	}
	if config.CleanupEnabled != true {
		t.Error("Expected CleanupEnabled true")
	}
	if config.CleanupInterval != 1*time.Hour {
		t.Errorf("Expected CleanupInterval 1h, got %v", config.CleanupInterval)
	}
	if config.RecordTTL != 7*24*time.Hour {
		t.Errorf("Expected RecordTTL 7 days, got %v", config.RecordTTL)
	}
}

func TestStorage_ConsumeQuota_InvalidAmount(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Negative amount
	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   -1, // Invalid
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	
	_, err := storage.ConsumeQuota(ctx, req)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount, got %v", err)
	}
}

func TestStorage_RefundQuota_InvalidAmount(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Negative amount
	req := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     -1, // Invalid
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
	}
	
	err := storage.RefundQuota(ctx, req)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount, got %v", err)
	}
}

func TestStorage_SetEntitlement_Invalid(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	// Nil entitlement
	err := storage.SetEntitlement(ctx, nil)
	if err == nil {
		t.Error("Expected error for nil entitlement")
	}
	
	// Empty user ID
	ent := &goquota.Entitlement{
		UserID: "", // Invalid
		Tier:   "scholar",
	}
	err = storage.SetEntitlement(ctx, ent)
	if err == nil {
		t.Error("Expected error for empty user ID")
	}
}

func TestStorage_SetUsage_NilUsage(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	err := storage.SetUsage(ctx, "user1", "api_calls", nil, period)
	if err == nil {
		t.Error("Expected error for nil usage")
	}
}

func TestStorage_GetConsumptionRecord_EmptyKey(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	record, err := storage.GetConsumptionRecord(ctx, "")
	if err != nil {
		t.Errorf("GetConsumptionRecord with empty key should not error, got %v", err)
	}
	if record != nil {
		t.Error("Expected nil record for empty key")
	}
}

func TestStorage_GetRefundRecord_EmptyKey(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	record, err := storage.GetRefundRecord(ctx, "")
	if err != nil {
		t.Errorf("GetRefundRecord with empty key should not error, got %v", err)
	}
	if record != nil {
		t.Error("Expected nil record for empty key")
	}
}

func TestStorage_RefundQuota_InvalidPeriodType(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	req := &goquota.RefundRequest{
		UserID:     "user1",
		Resource:   "api_calls",
		Amount:     10,
		PeriodType: goquota.PeriodType("invalid"), // Invalid period type
	}
	
	err := storage.RefundQuota(ctx, req)
	if err != goquota.ErrInvalidPeriod {
		t.Errorf("Expected ErrInvalidPeriod, got %v", err)
	}
}

// TestStorage_ConsumeQuota_RaceCondition_NewUser tests the UPSERT race condition
// where multiple goroutines try to create a usage record for a new user simultaneously
func TestStorage_ConsumeQuota_RaceCondition_NewUser(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Test concurrent consumption for a brand new user (tests UPSERT race condition)
	const numGoroutines = 50
	const amountPerRequest = 1
	const limit = 100
	
	results := make(chan int, numGoroutines)
	errors := make(chan error, numGoroutines)
	
	// All goroutines start simultaneously
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:   "new_user_race",
				Resource: "api_calls",
				Amount:   amountPerRequest,
				Tier:     "scholar",
				Period:   period,
				Limit:    limit,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				errors <- err
				return
			}
			results <- used
		}()
	}
	
	wg.Wait()
	close(results)
	close(errors)
	
	// Collect results
	var totalUsed int
	var errCount int
	for used := range results {
		totalUsed = used // Last value (should be cumulative)
	}
	for range errors {
		errCount++
	}
	
	// Verify final usage
	usage, err := storage.GetUsage(ctx, "new_user_race", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	
	expectedUsed := numGoroutines * amountPerRequest
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (race condition), got %d", expectedUsed, usage.Used)
	}
	if totalUsed != expectedUsed {
		t.Errorf("Expected total used %d, got %d", expectedUsed, totalUsed)
	}
	if errCount > 0 {
		t.Errorf("Unexpected errors in race condition test: %d errors", errCount)
	}
}

// TestStorage_ConsumeQuota_RaceCondition_ExactLimit tests concurrent consumption
// that exactly hits the limit
func TestStorage_ConsumeQuota_RaceCondition_ExactLimit(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	const limit = 100
	const numGoroutines = 100 // Exactly the limit
	
	// All goroutines try to consume 1 unit simultaneously
	results := make(chan int, numGoroutines)
	errors := make(chan error, numGoroutines)
	
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:   "user_exact_limit",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    limit,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				errors <- err
				return
			}
			results <- used
		}()
	}
	
	wg.Wait()
	close(results)
	close(errors)
	
	// Collect results
	successCount := 0
	var lastUsed int
	for used := range results {
		successCount++
		lastUsed = used
	}
	
	errorCount := 0
	for err := range errors {
		errorCount++
		if err != goquota.ErrQuotaExceeded {
			t.Errorf("Unexpected error: %v", err)
		}
	}
	
	// Verify: all should succeed (exactly at limit)
	if successCount != numGoroutines {
		t.Errorf("Expected all %d requests to succeed, got %d successes, %d errors",
			numGoroutines, successCount, errorCount)
	}
	if lastUsed != limit {
		t.Errorf("Expected final usage to be %d, got %d", limit, lastUsed)
	}
	
	// Verify final usage
	usage, err := storage.GetUsage(ctx, "user_exact_limit", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != limit {
		t.Errorf("Expected usage %d, got %d", limit, usage.Used)
	}
}

// TestStorage_ConsumeRefund_RaceCondition tests concurrent consume and refund operations
func TestStorage_ConsumeRefund_RaceCondition(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Initial consumption
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user_consume_refund",
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
	
	const consumeGoroutines = 50
	const refundGoroutines = 30
	consumeResults := make(chan int, consumeGoroutines)
	consumeErrors := make(chan error, consumeGoroutines)
	refundErrors := make(chan error, refundGoroutines)
	
	var wg sync.WaitGroup
	
	// Concurrent consumes
	for i := 0; i < consumeGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:   "user_consume_refund",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    1000,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				consumeErrors <- err
				return
			}
			consumeResults <- used
		}()
	}
	
	// Concurrent refunds
	for i := 0; i < refundGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refundReq := &goquota.RefundRequest{
				UserID:     "user_consume_refund",
				Resource:   "api_calls",
				Amount:     1,
				PeriodType: goquota.PeriodTypeDaily,
				Period:     period,
			}
			refundErrors <- storage.RefundQuota(ctx, refundReq)
		}()
	}
	
	wg.Wait()
	close(consumeResults)
	close(consumeErrors)
	close(refundErrors)
	
	// Count successes
	consumeSuccess := 0
	for range consumeResults {
		consumeSuccess++
	}
	
	consumeErrorsCount := 0
	for err := range consumeErrors {
		consumeErrorsCount++
		if err != goquota.ErrQuotaExceeded {
			t.Errorf("Unexpected consume error: %v", err)
		}
	}
	
	refundErrorsCount := 0
	for err := range refundErrors {
		refundErrorsCount++
		if err != nil {
			t.Errorf("Unexpected refund error: %v", err)
		}
	}
	
	// Verify final usage: 100 (initial) + 50 (consumes) - 30 (refunds) = 120
	usage, err := storage.GetUsage(ctx, "user_consume_refund", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	
	expectedUsed := 100 + consumeSuccess - refundGoroutines
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (100 + %d - %d), got %d",
			expectedUsed, consumeSuccess, refundGoroutines, usage.Used)
	}
}

// TestStorage_ConsumeQuota_RaceCondition_Idempotency tests concurrent requests
// with the same idempotency key
func TestStorage_ConsumeQuota_RaceCondition_Idempotency(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	const numGoroutines = 20
	const idempotencyKey = "race-idempotency-key"
	
	results := make(chan int, numGoroutines)
	errors := make(chan error, numGoroutines)
	
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	
	// All goroutines use the same idempotency key simultaneously
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:          "user_idempotency_race",
				Resource:        "api_calls",
				Amount:          10,
				Tier:            "scholar",
				Period:          period,
				Limit:           1000,
				IdempotencyKey:  idempotencyKey,
				IdempotencyKeyTTL: 24 * time.Hour,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				errors <- err
				return
			}
			results <- used
		}()
	}
	
	wg.Wait()
	close(results)
	close(errors)
	
	// Collect results - all should return the same value (idempotent)
	var allResults []int
	for used := range results {
		allResults = append(allResults, used)
	}
	
	// All results should be identical (idempotency)
	if len(allResults) > 0 {
		firstResult := allResults[0]
		for i, result := range allResults {
			if result != firstResult {
				t.Errorf("Idempotency race failed: result[%d]=%d, expected %d", i, result, firstResult)
			}
		}
	}
	
	// Verify usage was only incremented once
	usage, err := storage.GetUsage(ctx, "user_idempotency_race", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected 10 used (consumed once despite %d concurrent requests), got %d",
			numGoroutines, usage.Used)
	}
}

// TestStorage_RefundQuota_EdgeCase_NegativeUsage tests that refunds don't go below zero
func TestStorage_RefundQuota_EdgeCase_NegativeUsage(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Consume some quota
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user_negative",
		Resource: "api_calls",
		Amount:   10,
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	_, err := storage.ConsumeQuota(ctx, consumeReq)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}
	
	// Try to refund more than was consumed
	refundReq := &goquota.RefundRequest{
		UserID:     "user_negative",
		Resource:   "api_calls",
		Amount:     50, // More than consumed
		PeriodType: goquota.PeriodTypeDaily,
		Period:     period,
	}
	
	err = storage.RefundQuota(ctx, refundReq)
	if err != nil {
		t.Fatalf("RefundQuota failed: %v", err)
	}
	
	// Verify usage is floored at 0
	usage, err := storage.GetUsage(ctx, "user_negative", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 0 {
		t.Errorf("Expected 0 used (floored), got %d", usage.Used)
	}
}

// TestStorage_ConsumeQuota_EdgeCase_ZeroAmount tests that zero amount is a no-op
func TestStorage_ConsumeQuota_EdgeCase_ZeroAmount(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Consume zero amount
	req := &goquota.ConsumeRequest{
		UserID:   "user_zero",
		Resource: "api_calls",
		Amount:   0, // Zero amount
		Tier:     "scholar",
		Period:   period,
		Limit:    100,
	}
	
	used, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota with zero amount failed: %v", err)
	}
	if used != 0 {
		t.Errorf("Expected 0 used for zero amount, got %d", used)
	}
	
	// Verify no usage record was created
	usage, err := storage.GetUsage(ctx, "user_zero", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage != nil {
		t.Errorf("Expected nil usage for zero amount, got %+v", usage)
	}
}

// TestStorage_ConcurrentReadWrite tests concurrent reads during writes
func TestStorage_ConcurrentReadWrite(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	// Initial consumption
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user_read_write",
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
	
	const readers = 50
	const writers = 30
	readErrors := make(chan error, readers)
	writeResults := make(chan int, writers)
	
	var wg sync.WaitGroup
	
	// Concurrent readers
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := storage.GetUsage(ctx, "user_read_write", "api_calls", period)
			readErrors <- err
		}()
	}
	
	// Concurrent writers
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:   "user_read_write",
				Resource: "api_calls",
				Amount:   1,
				Tier:     "scholar",
				Period:   period,
				Limit:    1000,
			}
			used, err := storage.ConsumeQuota(ctx, req)
			if err != nil {
				readErrors <- err
				return
			}
			writeResults <- used
		}()
	}
	
	wg.Wait()
	close(readErrors)
	close(writeResults)
	
	// Check for read errors
	readErrorCount := 0
	for err := range readErrors {
		if err != nil {
			readErrorCount++
			t.Errorf("Read error: %v", err)
		}
	}
	
	// Count successful writes
	writeSuccess := 0
	for range writeResults {
		writeSuccess++
	}
	
	// Verify final usage: 100 (initial) + 30 (writes) = 130
	usage, err := storage.GetUsage(ctx, "user_read_write", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	
	expectedUsed := 100 + writeSuccess
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (100 + %d), got %d", expectedUsed, writeSuccess, usage.Used)
	}
	if readErrorCount > 0 {
		t.Errorf("Unexpected read errors: %d", readErrorCount)
	}
}

// TestStorage_MultipleUsers_Concurrent tests multiple users accessing the same resource
func TestStorage_MultipleUsers_Concurrent(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}
	
	const numUsers = 10
	const requestsPerUser = 5
	
	var wg sync.WaitGroup
	errors := make(chan error, numUsers*requestsPerUser)
	
	// Each user makes concurrent requests
	for userID := 0; userID < numUsers; userID++ {
		for reqID := 0; reqID < requestsPerUser; reqID++ {
			wg.Add(1)
			go func(uid, rid int) {
				defer wg.Done()
				req := &goquota.ConsumeRequest{
					UserID:   fmt.Sprintf("user_%d", uid),
					Resource: "api_calls", // Same resource for all users
					Amount:   1,
					Tier:     "scholar",
					Period:   period,
					Limit:    100,
				}
				_, err := storage.ConsumeQuota(ctx, req)
				if err != nil {
					errors <- err
				}
			}(userID, reqID)
		}
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	errorCount := 0
	for err := range errors {
		errorCount++
		if err != goquota.ErrQuotaExceeded {
			t.Errorf("Unexpected error: %v", err)
		}
	}
	
	// Verify each user's usage
	for userID := 0; userID < numUsers; userID++ {
		usage, err := storage.GetUsage(ctx, fmt.Sprintf("user_%d", userID), "api_calls", period)
		if err != nil {
			t.Fatalf("GetUsage failed for user_%d: %v", userID, err)
		}
		if usage.Used != requestsPerUser {
			t.Errorf("Expected user_%d to have %d used, got %d", userID, requestsPerUser, usage.Used)
		}
	}
}

