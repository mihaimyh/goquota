package revenuecat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestWebhookCallback_Success verifies callback is invoked with correct parameters
func TestWebhookCallback_Success(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	var callbackInvoked bool
	var capturedEvent billing.WebhookEvent
	var mu sync.Mutex

	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"premium": "pro",
		},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
			mu.Lock()
			defer mu.Unlock()
			callbackInvoked = true
			capturedEvent = event
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create webhook payload
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)

	// Process webhook event
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err != nil {
		t.Fatalf("processWebhookEvent failed: %v", err)
	}

	// Verify callback was invoked
	mu.Lock()
	defer mu.Unlock()
	if !callbackInvoked {
		t.Fatal("Callback was not invoked")
	}

	// Verify event parameters
	if capturedEvent.UserID != "test_user_123" {
		t.Errorf("Expected UserID test_user_123, got %s", capturedEvent.UserID)
	}
	if capturedEvent.PreviousTier != "explorer" {
		t.Errorf("Expected PreviousTier explorer, got %s", capturedEvent.PreviousTier)
	}
	if capturedEvent.NewTier != "pro" {
		t.Errorf("Expected NewTier pro, got %s", capturedEvent.NewTier)
	}
	if capturedEvent.Provider != providerName {
		t.Errorf("Expected Provider %s, got %s", providerName, capturedEvent.Provider)
	}
	if capturedEvent.EventType != "INITIAL_PURCHASE" {
		t.Errorf("Expected EventType INITIAL_PURCHASE, got %s", capturedEvent.EventType)
	}

	// Verify metadata
	if capturedEvent.Metadata == nil {
		t.Fatal("Expected metadata to be populated")
	}
	if capturedEvent.Metadata["product_id"] != "premium" {
		t.Errorf("Expected product_id premium, got %v", capturedEvent.Metadata["product_id"])
	}
	if capturedEvent.Metadata["entitlement_id"] != "premium" {
		t.Errorf("Expected entitlement_id premium, got %v", capturedEvent.Metadata["entitlement_id"])
	}
	if capturedEvent.Metadata["event_type"] != "INITIAL_PURCHASE" {
		t.Errorf("Expected event_type INITIAL_PURCHASE, got %v", capturedEvent.Metadata["event_type"])
	}
}

// TestWebhookCallback_Error verifies webhook returns error when callback fails
func TestWebhookCallback_Error(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	callbackError := errors.New("callback failed")

	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"premium": "pro",
		},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, _ billing.WebhookEvent) error {
			return callbackError
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create webhook payload
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)

	// Process webhook event - should return error from callback
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err == nil {
		t.Fatal("Expected error when callback fails")
	}
	if !errors.Is(err, callbackError) {
		t.Errorf("Expected callback error, got: %v", err)
	}

	// Verify entitlement was still updated (callback failure doesn't rollback DB)
	ent, getErr := manager.GetEntitlement(ctx, "test_user_123")
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != "pro" {
		t.Errorf("Expected tier pro, got %s", ent.Tier)
	}
}

// TestWebhookCallback_Nil verifies webhook succeeds when callback is nil
func TestWebhookCallback_Nil(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"premium": "pro",
		},
		WebhookSecret: "test_secret",
		// No callback configured
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create webhook payload
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)

	// Process webhook event - should succeed without callback
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err != nil {
		t.Fatalf("processWebhookEvent failed: %v", err)
	}

	// Verify entitlement was updated
	ent, getErr := manager.GetEntitlement(ctx, "test_user_123")
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != "pro" {
		t.Errorf("Expected tier pro, got %s", ent.Tier)
	}
}

// TestWebhookCallback_TierChange verifies previousTier and newTier are correct
func TestWebhookCallback_TierChange(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	var capturedEvent billing.WebhookEvent
	var mu sync.Mutex

	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"basic":   "basic",
			"premium": "pro",
		},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
			mu.Lock()
			defer mu.Unlock()
			capturedEvent = event
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Set initial entitlement to Basic
	initialEnt := &goquota.Entitlement{
		UserID:    "test_user_123",
		Tier:      "basic",
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	if err := manager.SetEntitlement(ctx, initialEnt); err != nil {
		t.Fatalf("Failed to set initial entitlement: %v", err)
	}

	// Create webhook payload for upgrade to Premium
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)

	// Process webhook event
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err != nil {
		t.Fatalf("processWebhookEvent failed: %v", err)
	}

	// Verify tier change was captured
	mu.Lock()
	defer mu.Unlock()
	if capturedEvent.PreviousTier != "basic" {
		t.Errorf("Expected PreviousTier basic, got %s", capturedEvent.PreviousTier)
	}
	if capturedEvent.NewTier != "pro" {
		t.Errorf("Expected NewTier pro, got %s", capturedEvent.NewTier)
	}
}

// TestWebhookCallback_IdempotencySkipsRetry verifies that if callback fails after SetEntitlement,
// a retry with the same event timestamp skips processing (and thus skips callback)
func TestWebhookCallback_IdempotencySkipsRetry(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	callCount := 0
	var mu sync.Mutex

	provider, err := NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"premium": "pro",
		},
		WebhookSecret: "test_secret",
		WebhookCallback: func(_ context.Context, _ billing.WebhookEvent) error {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount == 1 {
				// First call fails
				return errors.New("callback failed on first attempt")
			}
			// Subsequent calls succeed (but shouldn't be reached due to idempotency)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create webhook payload with specific timestamp
	eventTime := time.Now()
	payload := createTestPayload("test_user_123", "premium", eventTime)

	// First attempt - callback fails
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err == nil {
		t.Fatal("Expected error on first attempt")
	}

	mu.Lock()
	firstCallCount := callCount
	mu.Unlock()

	if firstCallCount != 1 {
		t.Errorf("Expected callback to be called once, got %d", firstCallCount)
	}

	// Verify entitlement was updated despite callback failure
	ent, getErr := manager.GetEntitlement(ctx, "test_user_123")
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != "pro" {
		t.Errorf("Expected tier pro, got %s", ent.Tier)
	}

	// Second attempt (retry) with same timestamp - should be skipped by idempotency
	err = provider.processWebhookEvent(ctx, &payload, "test_user_123")
	if err != nil {
		t.Fatalf("Expected no error on retry (idempotency skip), got: %v", err)
	}

	mu.Lock()
	finalCallCount := callCount
	mu.Unlock()

	// Callback should NOT have been called again due to idempotency
	if finalCallCount != 1 {
		t.Errorf("Expected callback to remain at 1 call (idempotency skip), got %d", finalCallCount)
	}
}
