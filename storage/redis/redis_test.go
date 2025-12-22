package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// setupTestRedis creates a Redis client for testing
// Requires Redis running on localhost:6379
func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use DB 15 for testing
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clear test database
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test database: %v", err)
	}

	return client
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		client  redis.UniversalClient
		config  Config
		wantErr bool
	}{
		{
			name:    "nil client",
			client:  nil,
			config:  DefaultConfig(),
			wantErr: true,
		},
		{
			name:    "valid client with default config",
			client:  redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
			config:  DefaultConfig(),
			wantErr: false,
		},
		{
			name:   "valid client with custom config",
			client: redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
			config: Config{
				KeyPrefix:      "test:",
				EntitlementTTL: 5 * time.Minute,
				UsageTTL:       10 * time.Minute,
				MaxRetries:     5,
			},
			wantErr: false,
		},
		{
			name:   "empty key prefix uses default",
			client: redis.NewClient(&redis.Options{Addr: "localhost:6379"}),
			config: Config{
				KeyPrefix: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage, err := New(tt.client, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if storage == nil {
					t.Error("New() returned nil storage")
					return
				}
				if storage.config.KeyPrefix == "" {
					t.Error("KeyPrefix should not be empty")
				}
				if storage.config.MaxRetries == 0 {
					t.Error("MaxRetries should not be zero")
				}
			}
		})
	}
}

func TestStorage_GetSetEntitlement(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()

	t.Run("get non-existent entitlement", func(t *testing.T) {
		_, err := storage.GetEntitlement(ctx, "nonexistent")
		if err != goquota.ErrEntitlementNotFound {
			t.Errorf("Expected ErrEntitlementNotFound, got %v", err)
		}
	})

	t.Run("set and get entitlement", func(t *testing.T) {
		ent := &goquota.Entitlement{
			UserID:                "user123",
			Tier:                  "pro",
			SubscriptionStartDate: time.Now().UTC(),
			UpdatedAt:             time.Now().UTC(),
		}

		if err := storage.SetEntitlement(ctx, ent); err != nil {
			t.Fatalf("SetEntitlement failed: %v", err)
		}

		retrieved, err := storage.GetEntitlement(ctx, "user123")
		if err != nil {
			t.Fatalf("GetEntitlement failed: %v", err)
		}

		if retrieved.UserID != ent.UserID {
			t.Errorf("UserID mismatch: got %s, want %s", retrieved.UserID, ent.UserID)
		}
		if retrieved.Tier != ent.Tier {
			t.Errorf("Tier mismatch: got %s, want %s", retrieved.Tier, ent.Tier)
		}
	})

	t.Run("set nil entitlement", func(t *testing.T) {
		err := storage.SetEntitlement(ctx, nil)
		if err == nil {
			t.Error("Expected error for nil entitlement")
		}
	})

	t.Run("set entitlement with empty userID", func(t *testing.T) {
		ent := &goquota.Entitlement{
			UserID: "",
			Tier:   "pro",
		}
		err := storage.SetEntitlement(ctx, ent)
		if err == nil {
			t.Error("Expected error for empty userID")
		}
	})
}

func TestStorage_GetSetUsage(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	t.Run("get non-existent usage", func(t *testing.T) {
		usage, err := storage.GetUsage(ctx, "user123", "api_calls", period)
		if err != nil {
			t.Errorf("GetUsage failed: %v", err)
		}
		if usage != nil {
			t.Error("Expected nil usage for non-existent key")
		}
	})

	t.Run("set and get usage", func(t *testing.T) {
		usage := &goquota.Usage{
			UserID:    "user123",
			Resource:  "api_calls",
			Used:      50,
			Limit:     100,
			Period:    period,
			Tier:      "pro",
			UpdatedAt: time.Now().UTC(),
		}

		if err := storage.SetUsage(ctx, "user123", "api_calls", usage, period); err != nil {
			t.Fatalf("SetUsage failed: %v", err)
		}

		retrieved, err := storage.GetUsage(ctx, "user123", "api_calls", period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}

		if retrieved.Used != usage.Used {
			t.Errorf("Used mismatch: got %d, want %d", retrieved.Used, usage.Used)
		}
		if retrieved.Limit != usage.Limit {
			t.Errorf("Limit mismatch: got %d, want %d", retrieved.Limit, usage.Limit)
		}
	})

	t.Run("set nil usage", func(t *testing.T) {
		err := storage.SetUsage(ctx, "user123", "api_calls", nil, period)
		if err == nil {
			t.Error("Expected error for nil usage")
		}
	})
}

func TestStorage_ConsumeQuota(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	t.Run("consume quota successfully", func(t *testing.T) {
		req := &goquota.ConsumeRequest{
			UserID:   "user1",
			Resource: "api_calls",
			Amount:   10,
			Tier:     "pro",
			Period:   period,
			Limit:    100,
		}

		newUsed, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Fatalf("ConsumeQuota failed: %v", err)
		}
		if newUsed != 10 {
			t.Errorf("Expected newUsed=10, got %d", newUsed)
		}
	})

	t.Run("consume quota multiple times", func(t *testing.T) {
		req := &goquota.ConsumeRequest{
			UserID:   "user2",
			Resource: "api_calls",
			Amount:   25,
			Tier:     "pro",
			Period:   period,
			Limit:    100,
		}

		// First consumption
		newUsed, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Fatalf("First ConsumeQuota failed: %v", err)
		}
		if newUsed != 25 {
			t.Errorf("Expected newUsed=25, got %d", newUsed)
		}

		// Second consumption
		newUsed, err = storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Fatalf("Second ConsumeQuota failed: %v", err)
		}
		if newUsed != 50 {
			t.Errorf("Expected newUsed=50, got %d", newUsed)
		}
	})

	t.Run("quota exceeded", func(t *testing.T) {
		req := &goquota.ConsumeRequest{
			UserID:   "user3",
			Resource: "api_calls",
			Amount:   150,
			Tier:     "free",
			Period:   period,
			Limit:    100,
		}

		newUsed, err := storage.ConsumeQuota(ctx, req)
		if err != goquota.ErrQuotaExceeded {
			t.Errorf("Expected ErrQuotaExceeded, got %v", err)
		}
		if newUsed != 0 {
			t.Errorf("Expected newUsed=0 on quota exceeded, got %d", newUsed)
		}
	})

	t.Run("negative amount", func(t *testing.T) {
		req := &goquota.ConsumeRequest{
			UserID:   "user4",
			Resource: "api_calls",
			Amount:   -10,
			Tier:     "pro",
			Period:   period,
			Limit:    100,
		}

		_, err := storage.ConsumeQuota(ctx, req)
		if err != goquota.ErrInvalidAmount {
			t.Errorf("Expected ErrInvalidAmount, got %v", err)
		}
	})

	t.Run("zero amount", func(t *testing.T) {
		req := &goquota.ConsumeRequest{
			UserID:   "user5",
			Resource: "api_calls",
			Amount:   0,
			Tier:     "pro",
			Period:   period,
			Limit:    100,
		}

		newUsed, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Errorf("Zero amount should not error: %v", err)
		}
		if newUsed != 0 {
			t.Errorf("Expected newUsed=0 for zero amount, got %d", newUsed)
		}
	})

	t.Run("with idempotency key - duplicate", func(t *testing.T) {
		idempotencyKey := "test-key-123"
		req1 := &goquota.ConsumeRequest{
			UserID:         "user6",
			Resource:       "api_calls",
			Amount:         10,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: idempotencyKey,
		}

		// First consumption
		newUsed1, err := storage.ConsumeQuota(ctx, req1)
		if err != nil {
			t.Fatalf("First ConsumeQuota failed: %v", err)
		}
		if newUsed1 != 10 {
			t.Errorf("Expected newUsed=10, got %d", newUsed1)
		}

		// Second consumption with same idempotency key - should return cached result
		req2 := &goquota.ConsumeRequest{
			UserID:         "user6",
			Resource:       "api_calls",
			Amount:         10,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: idempotencyKey,
		}

		newUsed2, err := storage.ConsumeQuota(ctx, req2)
		if err != nil {
			t.Fatalf("Second ConsumeQuota failed: %v", err)
		}
		if newUsed2 != 10 {
			t.Errorf("Expected cached newUsed=10, got %d", newUsed2)
		}

		// Verify usage was only consumed once
		usage, err := storage.GetUsage(ctx, "user6", "api_calls", period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}
		if usage.Used != 10 {
			t.Errorf("Expected usage 10 (consumed once), got %d", usage.Used)
		}

		// Verify consumption record exists
		record, err := storage.GetConsumptionRecord(ctx, idempotencyKey)
		if err != nil {
			t.Fatalf("GetConsumptionRecord failed: %v", err)
		}
		if record == nil {
			t.Fatal("Expected consumption record, got nil")
		}
		if record.NewUsed != 10 {
			t.Errorf("Expected NewUsed 10, got %d", record.NewUsed)
		}
	})

	t.Run("with idempotency key - different keys", func(t *testing.T) {
		// First consumption with idempotency key 1
		req1 := &goquota.ConsumeRequest{
			UserID:         "user7",
			Resource:       "api_calls",
			Amount:         5,
			Tier:           "pro",
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
			UserID:         "user7",
			Resource:       "api_calls",
			Amount:         5,
			Tier:           "pro",
			Period:         period,
			Limit:          100,
			IdempotencyKey: "key-2",
		}

		_, err = storage.ConsumeQuota(ctx, req2)
		if err != nil {
			t.Fatalf("Second ConsumeQuota failed: %v", err)
		}

		// Verify usage was consumed twice
		usage, err := storage.GetUsage(ctx, "user7", "api_calls", period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}
		if usage.Used != 10 {
			t.Errorf("Expected usage 10 (consumed twice), got %d", usage.Used)
		}
	})
}

func TestStorage_GetConsumptionRecord(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()

	t.Run("not found", func(t *testing.T) {
		record, err := storage.GetConsumptionRecord(ctx, "non-existent-key")
		if err != nil {
			t.Fatalf("GetConsumptionRecord failed: %v", err)
		}
		if record != nil {
			t.Errorf("Expected nil record, got %+v", record)
		}
	})
}

func TestStorage_ConsumeQuota_WithIdempotencyKey_Concurrent(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	idempotencyKey := "concurrent-idempotency-key"
	const goroutines = 10

	done := make(chan int, goroutines)
	errors := make(chan error, goroutines)

	// Launch concurrent requests with same idempotency key
	for i := 0; i < goroutines; i++ {
		go func() {
			req := &goquota.ConsumeRequest{
				UserID:         "concurrent_user",
				Resource:       "api_calls",
				Amount:         5,
				Tier:           "pro",
				Period:         period,
				Limit:          100,
				IdempotencyKey: idempotencyKey,
			}
			newUsed, err := storage.ConsumeQuota(ctx, req)
			done <- newUsed
			errors <- err
		}()
	}

	// Collect results
	var results []int
	for i := 0; i < goroutines; i++ {
		result := <-done
		err := <-errors
		if err != nil {
			t.Errorf("Concurrent ConsumeQuota failed: %v", err)
		}
		results = append(results, result)
	}

	// All should return the same result (idempotent)
	expected := results[0]
	for i, result := range results {
		if result != expected {
			t.Errorf("Result %d differs: got %d, expected %d", i, result, expected)
		}
	}

	// Verify usage was only consumed once
	usage, err := storage.GetUsage(ctx, "concurrent_user", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}
	if usage.Used != 5 {
		t.Errorf("Expected usage 5 (consumed once despite %d concurrent requests), got %d", goroutines, usage.Used)
	}
}

func TestStorage_ApplyTierChange(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	t.Run("apply tier change", func(t *testing.T) {
		// First consume some quota
		consumeReq := &goquota.ConsumeRequest{
			UserID:   "user1",
			Resource: "audio_seconds",
			Amount:   1000,
			Tier:     "free",
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
			OldTier:     "free",
			NewTier:     "pro",
			Period:      period,
			OldLimit:    3600,
			NewLimit:    18000,
			CurrentUsed: 1000,
		}

		if err := storage.ApplyTierChange(ctx, tierReq); err != nil {
			t.Fatalf("ApplyTierChange failed: %v", err)
		}

		// Verify the new limit
		usage, err := storage.GetUsage(ctx, "user1", "audio_seconds", period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}
		if usage.Limit != 18000 {
			t.Errorf("Expected limit=18000, got %d", usage.Limit)
		}
		if usage.Tier != "pro" {
			t.Errorf("Expected tier=pro, got %s", usage.Tier)
		}
	})
}

func TestStorage_RefundQuota(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	const testResourceAPICalls = "api_calls"
	t.Run("refund quota successfully", func(t *testing.T) {
		userID := "refund_user_1"
		resource := testResourceAPICalls

		// 1. Consume 100
		_, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
			UserID:   userID,
			Resource: resource,
			Amount:   100,
			Tier:     "pro",
			Period:   period,
			Limit:    1000,
		})
		if err != nil {
			t.Fatalf("Initial ConsumeQuota failed: %v", err)
		}

		// 2. Refund 50
		req := &goquota.RefundRequest{
			UserID:         userID,
			Resource:       resource,
			Amount:         50,
			PeriodType:     goquota.PeriodTypeMonthly,
			Period:         period,
			IdempotencyKey: "refund_id_1",
			Reason:         "test refund",
		}

		if err := storage.RefundQuota(ctx, req); err != nil {
			t.Fatalf("RefundQuota failed: %v", err)
		}

		// 3. Verify usage is 50
		usage, err := storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}
		if usage.Used != 50 {
			t.Errorf("Expected used=50 after refund, got %d", usage.Used)
		}

		// 4. Verify refund record exists
		record, err := storage.GetRefundRecord(ctx, "refund_id_1")
		if err != nil {
			t.Fatalf("GetRefundRecord failed: %v", err)
		}
		if record == nil {
			t.Fatal("Refund record not found")
		}
		if record.Amount != 50 {
			t.Errorf("Refund record amount mismatch: got %d, want 50", record.Amount)
		}
	})

	t.Run("idempotency check", func(t *testing.T) {
		userID := "refund_user_2"
		resource := testResourceAPICalls

		// 1. Consume 100
		_, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
			UserID:   userID,
			Resource: resource,
			Amount:   100,
			Tier:     "pro",
			Period:   period,
			Limit:    1000,
		})
		if err != nil {
			t.Fatalf("ConsumeQuota failed: %v", err)
		}

		// 2. First Refund
		req := &goquota.RefundRequest{
			UserID:         userID,
			Resource:       resource,
			Amount:         10,
			PeriodType:     goquota.PeriodTypeMonthly,
			Period:         period,
			IdempotencyKey: "refund_unique_key",
		}
		if err := storage.RefundQuota(ctx, req); err != nil {
			t.Fatalf("First RefundQuota failed: %v", err)
		}

		// 3. Second Refund (Same Key)
		if err := storage.RefundQuota(ctx, req); err != nil {
			t.Fatalf("Duplicate RefundQuota failed (should succeed silently): %v", err)
		}

		// 4. Verify usage decreased only once (100 - 10 = 90)
		usage, _ := storage.GetUsage(ctx, userID, resource, period)
		if usage.Used != 90 {
			t.Errorf("Expected used=90 after idempotent refund, got %d", usage.Used)
		}
	})

	t.Run("refund exceeds usage (full refund to 0)", func(t *testing.T) {
		userID := "refund_user_3"
		resource := testResourceAPICalls

		// Consume partial amount (50)
		if _, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
			UserID:   userID,
			Resource: "api_calls",
			Amount:   50,

			Period: period,
			Tier:   "free",
			Limit:  100,
		}); err != nil {
			t.Fatalf("ConsumeQuota failed: %v", err)
		}

		// 2. Refund 100 (more than used)
		req := &goquota.RefundRequest{
			UserID:         userID,
			Resource:       resource,
			Amount:         100,
			PeriodType:     goquota.PeriodTypeMonthly,
			Period:         period,
			IdempotencyKey: "refund_over",
		}
		if err := storage.RefundQuota(ctx, req); err != nil {
			t.Fatalf("RefundQuota failed: %v", err)
		}

		// 3. Verify usage is 0, not negative
		usage, _ := storage.GetUsage(ctx, userID, resource, period)
		if usage.Used != 0 {
			t.Errorf("Expected used=0 (clamped), got %d", usage.Used)
		}
	})
}

func TestStorage_Ping(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	if err := storage.Ping(ctx); err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestStorage_KeyGeneration(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	config := Config{
		KeyPrefix: "test:",
	}
	storage, err := New(client, config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	t.Run("entitlement key", func(t *testing.T) {
		key := storage.entitlementKey("user123")
		expected := "test:entitlement:user123"
		if key != expected {
			t.Errorf("Expected key=%s, got %s", expected, key)
		}
	})

	t.Run("usage key", func(t *testing.T) {
		period := goquota.Period{
			Start: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
			Type:  goquota.PeriodTypeMonthly,
		}
		key := storage.usageKey("user123", "api_calls", period)
		expected := "test:usage:user123:api_calls:2024-01-15"
		if key != expected {
			t.Errorf("Expected key=%s, got %s", expected, key)
		}
	})
}

func TestStorage_ConcurrentConsumption(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Concurrent consumption test
	const goroutines = 10
	const consumeAmount = 5
	const limit = 100

	done := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			req := &goquota.ConsumeRequest{
				UserID:   "concurrent_user",
				Resource: "api_calls",
				Amount:   consumeAmount,
				Tier:     "pro",
				Period:   period,
				Limit:    limit,
			}
			_, err := storage.ConsumeQuota(ctx, req)
			done <- err
		}()
	}

	// Wait for all goroutines
	for i := 0; i < goroutines; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent consumption failed: %v", err)
		}
	}

	// Verify final usage
	usage, err := storage.GetUsage(ctx, "concurrent_user", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	expectedUsed := goroutines * consumeAmount
	if usage.Used != expectedUsed {
		t.Errorf("Expected used=%d, got %d", expectedUsed, usage.Used)
	}
}

func TestStorage_TTL(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	config := Config{
		KeyPrefix:      "ttl_test:",
		EntitlementTTL: 1 * time.Second,
		UsageTTL:       1 * time.Second,
	}
	storage, err := New(client, config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()

	t.Run("entitlement TTL", func(t *testing.T) {
		ent := &goquota.Entitlement{
			UserID:    "ttl_user",
			Tier:      "pro",
			UpdatedAt: time.Now().UTC(),
		}

		if err := storage.SetEntitlement(ctx, ent); err != nil {
			t.Fatalf("SetEntitlement failed: %v", err)
		}

		// Check TTL is set
		key := storage.entitlementKey("ttl_user")
		ttl, err := client.TTL(ctx, key).Result()
		if err != nil {
			t.Fatalf("TTL check failed: %v", err)
		}
		if ttl <= 0 {
			t.Error("Expected positive TTL")
		}

		// Wait for expiration
		time.Sleep(2 * time.Second)

		_, err = storage.GetEntitlement(ctx, "ttl_user")
		if err != goquota.ErrEntitlementNotFound {
			t.Errorf("Expected ErrEntitlementNotFound after TTL, got %v", err)
		}
	})
}

func BenchmarkStorage_ConsumeQuota(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	storage, err := New(client, DefaultConfig())
	if err != nil {
		b.Fatalf("Failed to create storage: %v", err)
	}

	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "bench_user",
		Resource: "api_calls",
		Amount:   1,
		Tier:     "pro",
		Period:   period,
		Limit:    1000000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			b.Fatalf("ConsumeQuota failed: %v", err)
		}
	}
}

func TestStorage_EdgeCases(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Type:  goquota.PeriodTypeMonthly,
	}

	t.Run("refund non-existent user", func(t *testing.T) {
		req := &goquota.RefundRequest{
			UserID:         "ghost_user",
			Resource:       "api_calls",
			Amount:         50,
			PeriodType:     goquota.PeriodTypeMonthly,
			Period:         period,
			IdempotencyKey: "ghost_refund",
		}
		// Should not return error, should treated as valid but effective usage is 0
		if err := storage.RefundQuota(ctx, req); err != nil {
			t.Errorf("RefundQuota on non-existent user returned error: %v", err)
		}

		// Verify usage is 0 (or remains non-existent/0)
		usage, err := storage.GetUsage(ctx, "ghost_user", "api_calls", period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}
		if usage != nil && usage.Used != 0 {
			t.Errorf("Expected usage 0 or nil, got %v", usage)
		}
	})

	t.Run("concurrent mixed consume and refund", func(t *testing.T) {
		userID := "mixed_user"
		resource := "tokens"

		// Initial setup: 1000 tokens
		if _, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
			UserID:   userID,
			Resource: resource,
			Amount:   1000,
			Tier:     "pro",
			Period:   period,
			Limit:    10000,
		}); err != nil {
			t.Fatalf("Initial ConsumeQuota failed: %v", err)
		}

		const goroutines = 20
		const operations = 50

		// 10 routines consuming 1, 10 routines refunding 1
		errChan := make(chan error, goroutines*operations)

		for i := 0; i < goroutines/2; i++ {
			go func() {
				for j := 0; j < operations; j++ {
					_, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
						UserID:   userID,
						Resource: resource,
						Amount:   1,
						Tier:     "pro",
						Period:   period,
						Limit:    10000,
					})
					errChan <- err
				}
			}()
		}

		for i := 0; i < goroutines/2; i++ {
			go func() {
				for j := 0; j < operations; j++ {
					// Use unique key for each refund to ensure they all count
					err := storage.RefundQuota(ctx, &goquota.RefundRequest{
						UserID:         userID,
						Resource:       resource,
						Amount:         1, // Refund 1
						PeriodType:     goquota.PeriodTypeMonthly,
						Period:         period,
						IdempotencyKey: fmt.Sprintf("refund_%d_%d", i, j),
					})
					errChan <- err
				}
			}()
		}

		// Wait for all
		for i := 0; i < goroutines*operations; i++ {
			if err := <-errChan; err != nil {
				// QuotaExceeded is acceptable if we hit limit, but here limit is high
				t.Errorf("Concurrent operation failed: %v", err)
			}
		}

		// Total change should be 0 (Consume concurrent to Refund of same amount/count)
		// 10 * 50 * (+1) + 10 * 50 * (-1) = 0 change.
		// Start was 1000. End should be 1000.
		usage, err := storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}

		if usage.Used != 1000 {
			t.Errorf("Race condition detected! Expected 1000 used, got %d", usage.Used)
		}
	})

	t.Run("concurrent refunds with SAME idempotency key", func(t *testing.T) {
		userID := "idempotent_race_user"
		resource := "api_calls"

		// Consume 100
		if _, err := storage.ConsumeQuota(ctx, &goquota.ConsumeRequest{
			UserID:   userID,
			Resource: resource,
			Amount:   100,
			Tier:     "pro",
			Period:   period,
			Limit:    1000,
		}); err != nil {
			t.Fatalf("ConsumeQuota failed: %v", err)
		}

		const goroutines = 20
		errChan := make(chan error, goroutines)
		key := "race_conditions_key"

		for i := 0; i < goroutines; i++ {
			go func() {
				err := storage.RefundQuota(ctx, &goquota.RefundRequest{
					UserID:         userID,
					Resource:       resource,
					Amount:         50,
					PeriodType:     goquota.PeriodTypeMonthly,
					Period:         period,
					IdempotencyKey: key, // All share same key
				})
				errChan <- err
			}()
		}

		for i := 0; i < goroutines; i++ {
			if err := <-errChan; err != nil {
				t.Errorf("Concurrent refund failed: %v", err)
			}
		}

		// Usage should be 50 (100 - 50 once)
		usage, err := storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			t.Fatalf("GetUsage failed: %v", err)
		}

		if usage.Used != 50 {
			t.Errorf("Idempotency failed under concurrency! Expected 50 used, got %d", usage.Used)
		}
	})
}
