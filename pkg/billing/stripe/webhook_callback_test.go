package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v84"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestWebhookCallback_SubscriptionCreated_Success verifies callback is invoked with correct parameters
func TestWebhookCallback_SubscriptionCreated_Success(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	var callbackInvoked bool
	var capturedEvent billing.WebhookEvent
	var mu sync.Mutex

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDPro: testTierPro,
			},
			WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
				mu.Lock()
				defer mu.Unlock()
				callbackInvoked = true
				capturedEvent = event
				return nil
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create subscription created event
	now := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: now.Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id": testUserID,
			"custom":  "metadata",
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test",
		Type:    "customer.subscription.created",
		Created: now.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// Process event
	err = provider.handleSubscriptionCreated(ctx, event, time.Unix(event.Created, 0))
	if err != nil {
		t.Fatalf("handleSubscriptionCreated failed: %v", err)
	}

	// Verify callback was invoked
	mu.Lock()
	defer mu.Unlock()
	if !callbackInvoked {
		t.Fatal("Callback was not invoked")
	}

	// Verify event parameters
	if capturedEvent.UserID != testUserID {
		t.Errorf("Expected UserID %s, got %s", testUserID, capturedEvent.UserID)
	}
	if capturedEvent.PreviousTier != testTierExplorer {
		t.Errorf("Expected PreviousTier %s, got %s", testTierExplorer, capturedEvent.PreviousTier)
	}
	if capturedEvent.NewTier != testTierPro {
		t.Errorf("Expected NewTier %s, got %s", testTierPro, capturedEvent.NewTier)
	}
	if capturedEvent.Provider != providerName {
		t.Errorf("Expected Provider %s, got %s", providerName, capturedEvent.Provider)
	}
	if capturedEvent.EventType != string(event.Type) {
		t.Errorf("Expected EventType %s, got %s", event.Type, capturedEvent.EventType)
	}
	if capturedEvent.Metadata == nil {
		t.Fatal("Expected metadata to be populated")
	}
	if metadata, ok := capturedEvent.Metadata["subscription_metadata"].(map[string]string); !ok ||
		metadata["custom"] != "metadata" {
		t.Error("Expected subscription metadata to be included")
	}
}

// TestWebhookCallback_SubscriptionCreated_Error verifies webhook returns 500 when callback fails
func TestWebhookCallback_SubscriptionCreated_Error(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	callbackError := errors.New("callback failed")

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDPro: testTierPro,
			},
			WebhookCallback: func(_ context.Context, _ billing.WebhookEvent) error {
				return callbackError
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create subscription created event
	now := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: now.Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id": testUserID,
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test",
		Type:    "customer.subscription.created",
		Created: now.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// Process event - should return error from callback
	err = provider.handleSubscriptionCreated(ctx, event, time.Unix(event.Created, 0))
	if err == nil {
		t.Fatal("Expected error when callback fails")
	}
	if !errors.Is(err, callbackError) {
		t.Errorf("Expected callback error, got: %v", err)
	}

	// Verify entitlement was still updated (callback failure doesn't rollback DB)
	ent, getErr := manager.GetEntitlement(ctx, testUserID)
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != testTierPro {
		t.Errorf("Expected tier %s, got %s", testTierPro, ent.Tier)
	}
}

// TestWebhookCallback_SubscriptionCreated_Nil verifies webhook succeeds when callback is nil
func TestWebhookCallback_SubscriptionCreated_Nil(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDPro: testTierPro,
			},
			// No callback configured
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create subscription created event
	now := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: now.Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id": testUserID,
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test",
		Type:    "customer.subscription.created",
		Created: now.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// Process event - should succeed without callback
	err = provider.handleSubscriptionCreated(ctx, event, time.Unix(event.Created, 0))
	if err != nil {
		t.Fatalf("handleSubscriptionCreated failed: %v", err)
	}

	// Verify entitlement was updated
	ent, getErr := manager.GetEntitlement(ctx, testUserID)
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != testTierPro {
		t.Errorf("Expected tier %s, got %s", testTierPro, ent.Tier)
	}
}

// TestWebhookCallback_TierChange verifies previousTier and newTier are correct
func TestWebhookCallback_TierChange(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	var capturedEvent billing.WebhookEvent
	var mu sync.Mutex

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				testPriceIDPro:   testTierPro,
			},
			WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
				mu.Lock()
				defer mu.Unlock()
				capturedEvent = event
				return nil
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Set initial entitlement to Basic
	initialEnt := &goquota.Entitlement{
		UserID:    testUserID,
		Tier:      testTierBasic,
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	if err := manager.SetEntitlement(ctx, initialEnt); err != nil {
		t.Fatalf("Failed to set initial entitlement: %v", err)
	}

	// Create subscription updated event (upgrade to Pro)
	now := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: now.Add(-1 * time.Hour).Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id": testUserID,
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test",
		Type:    "customer.subscription.updated",
		Created: now.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// Process event
	err = provider.handleSubscriptionUpdated(ctx, event, time.Unix(event.Created, 0))
	if err != nil {
		t.Fatalf("handleSubscriptionUpdated failed: %v", err)
	}

	// Verify tier change was captured
	mu.Lock()
	defer mu.Unlock()
	if capturedEvent.PreviousTier != testTierBasic {
		t.Errorf("Expected PreviousTier %s, got %s", testTierBasic, capturedEvent.PreviousTier)
	}
	if capturedEvent.NewTier != testTierPro {
		t.Errorf("Expected NewTier %s, got %s", testTierPro, capturedEvent.NewTier)
	}
}

// TestWebhookCallback_EventMetadata verifies event type, timestamp, and metadata are passed correctly
func TestWebhookCallback_EventMetadata(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	var capturedEvent billing.WebhookEvent
	var mu sync.Mutex

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDPro: testTierPro,
			},
			WebhookCallback: func(_ context.Context, event billing.WebhookEvent) error {
				mu.Lock()
				defer mu.Unlock()
				capturedEvent = event
				return nil
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create event with specific timestamp and metadata
	eventTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: eventTime.Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id":     testUserID,
			"plan_name":   "Pro Plan",
			"custom_data": "test123",
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test_metadata",
		Type:    "customer.subscription.created",
		Created: eventTime.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// Process event
	err = provider.handleSubscriptionCreated(ctx, event, eventTime)
	if err != nil {
		t.Fatalf("handleSubscriptionCreated failed: %v", err)
	}

	// Verify event metadata
	mu.Lock()
	defer mu.Unlock()

	if capturedEvent.EventType != "customer.subscription.created" {
		t.Errorf("Expected EventType 'customer.subscription.created', got %s", capturedEvent.EventType)
	}

	if !capturedEvent.EventTimestamp.Equal(eventTime) {
		t.Errorf("Expected EventTimestamp %v, got %v", eventTime, capturedEvent.EventTimestamp)
	}

	if capturedEvent.Metadata == nil {
		t.Fatal("Expected metadata to be populated")
	}

	subMetadata, ok := capturedEvent.Metadata["subscription_metadata"].(map[string]string)
	if !ok {
		t.Fatal("Expected subscription_metadata in Metadata")
	}

	if subMetadata["plan_name"] != "Pro Plan" {
		t.Errorf("Expected plan_name 'Pro Plan', got %s", subMetadata["plan_name"])
	}
	if subMetadata["custom_data"] != "test123" {
		t.Errorf("Expected custom_data 'test123', got %s", subMetadata["custom_data"])
	}
}

// TestWebhookCallback_IdempotencySkipsRetry verifies that if callback fails after SetEntitlement,
// a retry with the same event timestamp skips processing (and thus skips callback)
func TestWebhookCallback_IdempotencySkipsRetry(t *testing.T) {
	manager := mockManager(t)
	ctx := context.Background()

	callCount := 0
	var mu sync.Mutex

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDPro: testTierPro,
			},
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
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Create subscription created event
	eventTime := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: eventTime.Unix(),
		Customer: &stripe.Customer{
			ID: testCustomerID,
		},
		Metadata: map[string]string{
			"user_id": testUserID,
		},
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{
					Price: &stripe.Price{
						ID: testPriceIDPro,
					},
				},
			},
		},
	}

	eventData, _ := json.Marshal(sub)
	event := &stripe.Event{
		ID:      "evt_test",
		Type:    "customer.subscription.created",
		Created: eventTime.Unix(),
		Data: &stripe.EventData{
			Raw: eventData,
		},
	}

	// First attempt - callback fails
	err = provider.handleSubscriptionCreated(ctx, event, eventTime)
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
	ent, getErr := manager.GetEntitlement(ctx, testUserID)
	if getErr != nil {
		t.Fatalf("Failed to get entitlement: %v", getErr)
	}
	if ent.Tier != testTierPro {
		t.Errorf("Expected tier %s, got %s", testTierPro, ent.Tier)
	}

	// Second attempt (retry) with same timestamp - should be skipped by idempotency
	err = provider.handleSubscriptionCreated(ctx, event, eventTime)
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
