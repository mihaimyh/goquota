package redis

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/redis/go-redis/v9"
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
