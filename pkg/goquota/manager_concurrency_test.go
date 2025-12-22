package goquota_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Phase 3.2: Manager Concurrency Tests

const (
	testResourceAPICalls = "api_calls"
	testTierFluent       = "fluent"
)

func TestManager_ConcurrentConsumeRefund(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	userID := "user_concurrent"
	resource := testResourceAPICalls

	// Set entitlement with tier that has enough quota
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Initial consumption - start with 400 to leave room for operations
	// Scholar tier has 500 daily limit, so we can consume 100 more
	_, err = manager.Consume(ctx, userID, resource, 400, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("Initial Consume failed: %v", err)
	}

	const consumeGoroutines = 100
	const refundGoroutines = 50
	errChan := make(chan error, consumeGoroutines+refundGoroutines)
	successChan := make(chan bool, consumeGoroutines+refundGoroutines)

	// Concurrent consumes
	for i := 0; i < consumeGoroutines; i++ {
		go func() {
			_, err := manager.Consume(ctx, userID, resource, 1, goquota.PeriodTypeDaily)
			errChan <- err
			successChan <- (err == nil)
		}()
	}

	// Concurrent refunds
	for i := 0; i < refundGoroutines; i++ {
		go func() {
			refundReq := &goquota.RefundRequest{
				UserID:     userID,
				Resource:   resource,
				Amount:     1,
				PeriodType: goquota.PeriodTypeDaily,
			}
			err := manager.Refund(ctx, refundReq)
			errChan <- err
			successChan <- (err == nil)
		}()
	}

	// Collect results
	successfulConsumes := 0
	successfulRefunds := 0
	for i := 0; i < consumeGoroutines+refundGoroutines; i++ {
		err := <-errChan
		success := <-successChan
		if err != nil && err != goquota.ErrQuotaExceeded {
			t.Errorf("Concurrent operation %d failed with unexpected error: %v", i, err)
		}
		if i < consumeGoroutines && success {
			successfulConsumes++
		} else if i >= consumeGoroutines && success {
			successfulRefunds++
		}
	}

	// Verify final usage (400 + successfulConsumes - successfulRefunds)
	usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	expectedUsed := 400 + successfulConsumes - successfulRefunds
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used (400 + %d consumes - %d refunds), got %d",
			expectedUsed, successfulConsumes, successfulRefunds, usage.Used)
	}

	// Verify that we got close to expected (some consumes may fail due to quota limits)
	// With 400 initial + up to 100 consumes - 50 refunds, we should have at least 350
	if usage.Used < 350 {
		t.Errorf("Final usage %d seems too low, expected at least 350", usage.Used)
	}
}

func TestManager_ConcurrentCacheInvalidation(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	userID := "user_cache"
	resource := testResourceAPICalls

	// Set entitlement with tier that has enough quota
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Initial consumption
	_, err = manager.Consume(ctx, userID, resource, 100, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("Initial Consume failed: %v", err)
	}

	// Get quota to populate cache
	_, err = manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)
	var wg sync.WaitGroup

	// Concurrent operations that invalidate cache
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				// Consume (invalidates cache)
				_, err := manager.Consume(ctx, userID, resource, 1, goquota.PeriodTypeDaily)
				errChan <- err
			} else {
				// GetQuota (may hit cache or miss)
				_, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Collect errors
	for err := range errChan {
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
		}
	}

	// Verify final state
	usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	// Should have consumed 25 times (50/2)
	expectedUsed := 100 + 25
	if usage.Used != expectedUsed {
		t.Errorf("Expected %d used, got %d", expectedUsed, usage.Used)
	}
}

func TestManager_ConcurrentEntitlementUpdate(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	userID := "user_entitlement"
	resource := testResourceAPICalls

	// Set initial entitlement
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	err := manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	const goroutines = 50
	errChan := make(chan error, goroutines*2)

	// Concurrent entitlement updates and consumes
	for i := 0; i < goroutines; i++ {
		go func(_ int) {
			// Update entitlement
			newEnt := &goquota.Entitlement{
				UserID:                userID,
				Tier:                  testTierFluent,
				SubscriptionStartDate: time.Now().UTC(),
				UpdatedAt:             time.Now().UTC(),
			}
			errChan <- manager.SetEntitlement(ctx, newEnt)
		}(i)

		go func() {
			// Consume (reads entitlement)
			_, err := manager.Consume(ctx, userID, resource, 1, goquota.PeriodTypeDaily)
			errChan <- err
		}()
	}

	// Collect errors
	for i := 0; i < goroutines*2; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent operation %d failed: %v", i, err)
		}
	}

	// Verify entitlement was updated
	retrieved, err := manager.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("GetEntitlement failed: %v", err)
	}
	if retrieved.Tier != testTierFluent {
		t.Errorf("Expected tier fluent, got %s", retrieved.Tier)
	}
}

func TestManager_ConcurrentGetQuota(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	userID := "user_getquota"
	resource := testResourceAPICalls

	// Set entitlement with tier that has enough quota
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Initial consumption
	_, err = manager.Consume(ctx, userID, resource, 100, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("Initial Consume failed: %v", err)
	}

	const goroutines = 100
	usageChan := make(chan *goquota.Usage, goroutines)
	errChan := make(chan error, goroutines)

	// Concurrent GetQuota calls
	for i := 0; i < goroutines; i++ {
		go func() {
			usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
			usageChan <- usage
			errChan <- err
		}()
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent GetQuota %d failed: %v", i, err)
		}
		usage := <-usageChan
		if usage == nil {
			t.Errorf("Concurrent GetQuota %d returned nil", i)
		} else if usage.Used != 100 {
			t.Errorf("Concurrent GetQuota %d: expected 100 used, got %d", i, usage.Used)
		}
	}
}
