package goquota_test

import (
	"context"
	"testing"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func newScopedKeyManager(t *testing.T) *goquota.Manager {
	t.Helper()

	manager, err := goquota.NewManager(memory.New(), &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

// TestManager_Consume_IdempotencyKeySegmentsAreEscaped guards the injectivity of
// the scoped idempotency key: ("a:b", "c") and ("a", "b:c") used to join to the
// same record, so the second consume was replayed instead of charged.
func TestManager_Consume_IdempotencyKeySegmentsAreEscaped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	manager := newScopedKeyManager(t)

	if _, err := manager.Consume(ctx, "a:b", "api_calls", 1,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey("c")); err != nil {
		t.Fatalf("consume for \"a:b\": %v", err)
	}

	if _, err := manager.Consume(ctx, "a", "api_calls", 1,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey("b:c")); err != nil {
		t.Fatalf("consume for \"a\": %v", err)
	}

	usageAB, err := manager.GetQuota(ctx, "a:b", "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota for \"a:b\": %v", err)
	}
	if usageAB.Used != 1 {
		t.Fatalf("used for \"a:b\" = %d, want 1", usageAB.Used)
	}

	usageA, err := manager.GetQuota(ctx, "a", "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota for \"a\": %v", err)
	}
	if usageA.Used != 1 {
		t.Fatalf("used for \"a\" = %d, want 1 (key collision replayed the other user's consume)", usageA.Used)
	}
}

// TestManager_TopUpLimit_IdempotencyKeyScopedByUserAndResource guards the top-up
// key scope: the same caller key for two users (or two resources) must grant
// credits twice, while replaying the same (user, resource, key) must not.
func TestManager_TopUpLimit_IdempotencyKeyScopedByUserAndResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	manager := newScopedKeyManager(t)
	const key = "shared-topup-key"

	for _, userID := range []string{"user1", "user2"} {
		if err := manager.TopUpLimit(ctx, userID, "credits", 5,
			goquota.WithTopUpIdempotencyKey(key)); err != nil {
			t.Fatalf("TopUpLimit for %s: %v", userID, err)
		}
	}

	for _, userID := range []string{"user1", "user2"} {
		usage, err := manager.GetQuota(ctx, userID, "credits", goquota.PeriodTypeForever)
		if err != nil {
			t.Fatalf("GetQuota for %s: %v", userID, err)
		}
		if usage.Limit != 5 {
			t.Fatalf("limit for %s = %d, want 5 (top-up replayed across users)", userID, usage.Limit)
		}
	}

	// Same user and key again - idempotent replay, no double grant.
	if err := manager.TopUpLimit(ctx, "user1", "credits", 5,
		goquota.WithTopUpIdempotencyKey(key)); err != nil {
		t.Fatalf("replayed TopUpLimit: %v", err)
	}
	usage, err := manager.GetQuota(ctx, "user1", "credits", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota after replay: %v", err)
	}
	if usage.Limit != 5 {
		t.Fatalf("limit after replay = %d, want 5", usage.Limit)
	}

	// A different resource with the same key is a separate grant.
	if err := manager.TopUpLimit(ctx, "user1", "other_credits", 7,
		goquota.WithTopUpIdempotencyKey(key)); err != nil {
		t.Fatalf("TopUpLimit for other resource: %v", err)
	}
	other, err := manager.GetQuota(ctx, "user1", "other_credits", goquota.PeriodTypeForever)
	if err != nil {
		t.Fatalf("GetQuota for other resource: %v", err)
	}
	if other.Limit != 7 {
		t.Fatalf("limit for other resource = %d, want 7", other.Limit)
	}
}
