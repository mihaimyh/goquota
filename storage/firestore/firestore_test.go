package firestore

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

const (
	testProjectID = "test-project"
	emulatorHost  = "localhost:8080"
)

func setupFirestoreClient(t *testing.T) *firestore.Client {
	t.Helper()

	// Set emulator environment variable
	os.Setenv("FIRESTORE_EMULATOR_HOST", emulatorHost)

	ctx := context.Background()
	client, err := firestore.NewClient(ctx, testProjectID)
	if err != nil {
		t.Fatalf("Failed to create Firestore client: %v", err)
	}

	return client
}

// getTestCollections returns unique collection names for each test run
func getTestCollections(testName string) (string, string) {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("test_ent_%s_%d", testName, timestamp),
		fmt.Sprintf("test_usage_%s_%d", testName, timestamp)
}

func cleanupFirestore(t *testing.T, client *firestore.Client, collections ...string) {
	t.Helper()
	ctx := context.Background()

	for _, coll := range collections {
		// Delete all documents in collection
		iter := client.Collection(coll).Documents(ctx)
		bw := client.BulkWriter(ctx)

		for {
			doc, err := iter.Next()
			if err != nil {
				break
			}
			_, _ = bw.Delete(doc.Ref)
		}
		bw.Flush()

		// Also delete subcollections
		docs, _ := client.Collection(coll).Documents(ctx).GetAll()
		for _, doc := range docs {
			subcollections, _ := doc.Ref.Collections(ctx).GetAll()
			for _, subcoll := range subcollections {
				subIter := subcoll.Documents(ctx)
				for {
					subDoc, err := subIter.Next()
					if err != nil {
						break
					}
					_, _ = subDoc.Ref.Delete(ctx)
				}
			}
		}
	}
}

func TestFirestore_GetSetEntitlement(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("get_set_ent")

	storage, err := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	defer cleanupFirestore(t, client, entColl, usageColl)

	ctx := context.Background()

	// Test getting non-existent entitlement
	_, err = storage.GetEntitlement(ctx, "user1")
	if err != goquota.ErrEntitlementNotFound {
		t.Errorf("Expected ErrEntitlementNotFound, got %v", err)
	}

	// Test setting entitlement
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "pro",
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

	if retrieved.UserID != ent.UserID || retrieved.Tier != ent.Tier {
		t.Errorf("Retrieved entitlement mismatch: got %+v, want %+v", retrieved, ent)
	}
}

func TestFirestore_ConsumeQuota_Success(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ConsumeQuota_Success")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "pro",
		Period:   period,
		Limit:    1000,
	}

	_, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Verify usage
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Used != 50 {
		t.Errorf("Expected 50 used, got %d", usage.Used)
	}
}

func TestFirestore_ConsumeQuota_Exceeds(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ConsumeQuota_Exceeds")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	req := &goquota.ConsumeRequest{
		UserID:   "user1",
		Resource: "api_calls",
		Amount:   1500,
		Tier:     "pro",
		Period:   period,
		Limit:    1000,
	}

	_, err := storage.ConsumeQuota(ctx, req)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}

	// Verify no usage was recorded (or usage is still 0)
	usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	// Usage might be nil or have 0 used
	if usage != nil && usage.Used > 0 {
		t.Errorf("Expected 0 used after quota exceeded, got %d", usage.Used)
	}
}

func TestFirestore_ConsumeQuota_Concurrent(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ConsumeQuota_Concurrent")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Concurrent consumption
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &goquota.ConsumeRequest{
				UserID:   "user_concurrent",
				Resource: "api_calls",
				Amount:   10,
				Tier:     "pro",
				Period:   period,
				Limit:    1000,
			}
			if _, err := storage.ConsumeQuota(ctx, req); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent consumption error: %v", err)
	}

	// Verify total usage
	usage, err := storage.GetUsage(ctx, "user_concurrent", "api_calls", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Used != 100 {
		t.Errorf("Expected 100 used (10 x 10), got %d", usage.Used)
	}
}

func TestFirestore_ConsumeQuota_NearLimit(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ConsumeQuota_NearLimit")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume up to limit
	req := &goquota.ConsumeRequest{
		UserID:   "user_limit",
		Resource: "api_calls",
		Amount:   990,
		Tier:     "pro",
		Period:   period,
		Limit:    1000,
	}

	_, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("ConsumeQuota failed: %v", err)
	}

	// Try to consume more (should succeed - exactly at limit)
	req.Amount = 10
	_, err = storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Errorf("Expected success at limit, got %v", err)
	}

	// Try to consume one more (should fail)
	req.Amount = 1
	_, err = storage.ConsumeQuota(ctx, req)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}
}

func TestFirestore_ApplyTierChange(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ApplyTierChange")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume some quota first
	consumeReq := &goquota.ConsumeRequest{
		UserID:   "user_tier",
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

	// Apply tier change (upgrade)
	tierChangeReq := &goquota.TierChangeRequest{
		UserID:      "user_tier",
		Resource:    "audio_seconds",
		OldTier:     "scholar",
		NewTier:     "fluent",
		Period:      period,
		OldLimit:    3600,
		NewLimit:    18000,
		CurrentUsed: 1000,
	}

	err = storage.ApplyTierChange(ctx, tierChangeReq)
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Verify new limit
	usage, err := storage.GetUsage(ctx, "user_tier", "audio_seconds", period)
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if usage.Limit != 18000 {
		t.Errorf("Expected new limit 18000, got %d", usage.Limit)
	}
	if usage.Tier != "fluent" {
		t.Errorf("Expected tier fluent, got %s", usage.Tier)
	}
	if usage.Used != 1000 {
		t.Errorf("Expected used to remain 1000, got %d", usage.Used)
	}
}

func TestFirestore_MultipleResources(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_MultipleResources")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	period := goquota.Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume different resources
	resources := []string{"api_calls", "audio_seconds", "translations"}

	for _, resource := range resources {
		req := &goquota.ConsumeRequest{
			UserID:   "user_multi",
			Resource: resource,
			Amount:   100,
			Tier:     "pro",
			Period:   period,
			Limit:    1000,
		}

		_, err := storage.ConsumeQuota(ctx, req)
		if err != nil {
			t.Errorf("ConsumeQuota for %s failed: %v", resource, err)
		}
	}

	// Verify each resource independently
	for _, resource := range resources {
		usage, err := storage.GetUsage(ctx, "user_multi", resource, period)
		if err != nil {
			t.Errorf("GetUsage for %s failed: %v", resource, err)
		}
		if usage.Used != 100 {
			t.Errorf("Expected 100 used for %s, got %d", resource, usage.Used)
		}
	}
}

func TestFirestore_DifferentPeriods(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_DifferentPeriods")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})

	cleanupFirestore(t, client, "test_entitlements", "test_usage")
	defer cleanupFirestore(t, client, "test_entitlements", "test_usage")

	ctx := context.Background()
	now := time.Now().UTC()

	// Daily period - starts today
	dailyPeriod := goquota.Period{
		Start: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
		End:   time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour),
		Type:  goquota.PeriodTypeDaily,
	}

	// Monthly period - starts on the 1st of this month
	monthlyPeriod := goquota.Period{
		Start: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Add(30 * 24 * time.Hour),
		Type:  goquota.PeriodTypeMonthly,
	}

	// Consume in daily period
	dailyReq := &goquota.ConsumeRequest{
		UserID:   "user_periods",
		Resource: "api_calls",
		Amount:   50,
		Tier:     "pro",
		Period:   dailyPeriod,
		Limit:    100,
	}

	_, err := storage.ConsumeQuota(ctx, dailyReq)
	if err != nil {
		t.Fatalf("Daily ConsumeQuota failed: %v", err)
	}

	// Consume in monthly period
	monthlyReq := &goquota.ConsumeRequest{
		UserID:   "user_periods",
		Resource: "api_calls",
		Amount:   500,
		Tier:     "pro",
		Period:   monthlyPeriod,
		Limit:    10000,
	}

	_, err = storage.ConsumeQuota(ctx, monthlyReq)
	if err != nil {
		t.Fatalf("Monthly ConsumeQuota failed: %v", err)
	}

	// Verify both periods are independent
	dailyUsage, _ := storage.GetUsage(ctx, "user_periods", "api_calls", dailyPeriod)
	monthlyUsage, _ := storage.GetUsage(ctx, "user_periods", "api_calls", monthlyPeriod)

	if dailyUsage.Used != 50 {
		t.Errorf("Expected daily usage 50, got %d", dailyUsage.Used)
	}
	if monthlyUsage.Used != 500 {
		t.Errorf("Expected monthly usage 500, got %d", monthlyUsage.Used)
	}
}

func TestFirestore_ConsumeQuota_WithIdempotencyKey(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_ConsumeQuota_WithIdempotencyKey")
	refundsColl, consumptionsColl := getTestCollections("TestFirestore_ConsumeQuota_WithIdempotencyKey_refunds_consumptions")

	storage, _ := New(client, Config{
		EntitlementsCollection:  entColl,
		UsageCollection:          usageColl,
		RefundsCollection:        refundsColl,
		ConsumptionsCollection:   consumptionsColl,
	})

	defer cleanupFirestore(t, client, entColl, usageColl, refundsColl, consumptionsColl)

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
}

func TestFirestore_GetConsumptionRecord_NotFound(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	entColl, usageColl := getTestCollections("TestFirestore_GetConsumptionRecord_NotFound")
	_, consumptionsColl := getTestCollections("TestFirestore_GetConsumptionRecord_NotFound_consumptions")

	storage, _ := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
		ConsumptionsCollection: consumptionsColl,
	})

	defer cleanupFirestore(t, client, entColl, usageColl, consumptionsColl)

	ctx := context.Background()

	record, err := storage.GetConsumptionRecord(ctx, "non-existent-key")
	if err != nil {
		t.Fatalf("GetConsumptionRecord failed: %v", err)
	}
	if record != nil {
		t.Errorf("Expected nil record, got %+v", record)
	}
}