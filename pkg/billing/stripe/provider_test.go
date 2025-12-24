package stripe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v83"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

const (
	testStripeAPIKey        = "sk_test_1234567890"
	testStripeWebhookSecret = "whsec_test_secret"
	testUserID              = "test-user-123"
	testCustomerID          = "cus_test_123"
	testPriceIDBasic        = "price_basic_monthly"
	testPriceIDPro          = "price_pro_monthly"
	testTierBasic           = "basic"
	testTierPro             = "pro"
	testTierExplorer        = "explorer"
)

// mockManager creates a test manager for testing
func mockManager(t *testing.T) *goquota.Manager {
	t.Helper()
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: testTierExplorer,
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: testTierExplorer,
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
			"basic": {
				Name: testTierBasic,
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
			"pro": {
				Name: testTierPro,
				MonthlyQuotas: map[string]int{
					"api_calls": 10000,
				},
			},
		},
	}
	manager, err := goquota.NewManager(storage, config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	return manager
}

func TestProvider_Name(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if provider.Name() != providerName {
		t.Errorf("Expected name %s, got %s", providerName, provider.Name())
	}
}

func TestProvider_WebhookHandler(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	handler := provider.WebhookHandler()
	if handler == nil {
		t.Error("Expected webhook handler, got nil")
	}
}

func TestProvider_MapPriceToTier(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				testPriceIDPro:   testTierPro,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	tests := []struct {
		priceID  string
		expected string
	}{
		{testPriceIDBasic, testTierBasic},
		{testPriceIDPro, testTierPro},
		{"unknown_price", testTierExplorer},
		{"", testTierExplorer},
	}

	for _, tt := range tests {
		tier := provider.MapPriceToTier(tt.priceID)
		if tier != tt.expected {
			t.Errorf("MapPriceToTier(%s) = %s, expected %s", tt.priceID, tier, tt.expected)
		}
	}
}

func TestProvider_GetTierWeight(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				testPriceIDPro:   testTierPro,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
		TierWeights: map[string]int{
			testTierBasic: 50,
			testTierPro:   100,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	tests := []struct {
		tier     string
		expected int
	}{
		{testTierBasic, 50},
		{testTierPro, 100},
		{testTierExplorer, 0},
		{"unknown", 0},
	}

	for _, tt := range tests {
		weight := provider.GetTierWeight(tt.tier)
		if weight != tt.expected {
			t.Errorf("GetTierWeight(%s) = %d, expected %d", tt.tier, weight, tt.expected)
		}
	}
}

func TestProvider_NewProvider_InvalidConfig(t *testing.T) {
	manager := mockManager(t)

	// Test missing manager
	_, err := NewProvider(Config{
		Config: billing.Config{
			Manager: nil,
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err == nil {
		t.Error("Expected error for missing manager")
	}

	// Test missing API key
	_, err = NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
		},
		StripeAPIKey:        "",
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err == nil {
		t.Error("Expected error for missing API key")
	}
}

func TestProvider_ExtractTierFromSubscription(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				testPriceIDPro:   testTierPro,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
		TierWeights: map[string]int{
			testTierBasic: 50,
			testTierPro:   100,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	now := time.Now()
	sub := &stripe.Subscription{
		ID:      "sub_test",
		Status:  "active",
		Created: now.Unix(),
		// Note: CurrentPeriodStart/End are not in the Subscription struct
		// Period dates are extracted from webhook event JSON in production code
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

	tier, expiresAt, startDate := provider.extractTierFromSubscription(sub)
	if tier != testTierPro {
		t.Errorf("Expected tier %s, got %s", testTierPro, tier)
	}
	// Period dates are not extracted from Subscription struct (fields don't exist in v83)
	// They are set via webhook events which include current_period_start/end in JSON payload
	if expiresAt != nil {
		t.Error("Expected expiresAt to be nil (period dates come from webhook events)")
	}
	if startDate != nil {
		t.Error("Expected startDate to be nil (period dates come from webhook events)")
	}
}

func TestProvider_ExtractUserIDFromSubscription(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Test with subscription metadata
	sub := &stripe.Subscription{
		ID: "sub_test",
		Metadata: map[string]string{
			"user_id": testUserID,
		},
	}

	userID, err := provider.extractUserIDFromSubscription(context.Background(), sub)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if userID != testUserID {
		t.Errorf("Expected userID %s, got %s", testUserID, userID)
	}

	// Test with missing metadata
	sub.Metadata = nil
	_, err = provider.extractUserIDFromSubscription(context.Background(), sub)
	if err == nil {
		t.Error("Expected error for missing metadata")
	}
}

func TestProvider_WebhookHandler_MethodNotAllowed(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webhook", http.NoBody)
	w := httptest.NewRecorder()
	provider.handleWebhook(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestProvider_WebhookHandler_NoSecret(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: "", // Empty secret
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	w := httptest.NewRecorder()
	provider.handleWebhook(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestProvider_CustomerIDResolver(t *testing.T) {
	manager := mockManager(t)

	resolver := func(_ context.Context, userID string) (string, error) {
		if userID == testUserID {
			return testCustomerID, nil
		}
		return "", billing.ErrUserNotFound
	}

	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
		CustomerIDResolver:  resolver,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Verify resolver is set
	if provider.customerIDResolver == nil {
		t.Error("Expected CustomerIDResolver to be set")
	}

	// Note: Actual sync testing would require mocking Stripe API calls
	// which is complex. This test verifies the resolver is properly configured.
}

func TestProvider_TierWeights_AutoAssignment(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				testPriceIDPro:   testTierPro,
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
		// No TierWeights provided - should auto-assign
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Verify weights are auto-assigned (first = 100, second = 90, etc.)
	// Note: Order in map is non-deterministic, so we just check that weights exist
	basicWeight := provider.GetTierWeight(testTierBasic)
	proWeight := provider.GetTierWeight(testTierPro)

	if basicWeight == 0 && proWeight == 0 {
		t.Error("Expected at least one tier to have non-zero weight")
	}

	// Default tier should always be 0
	explorerWeight := provider.GetTierWeight(testTierExplorer)
	if explorerWeight != 0 {
		t.Errorf("Expected default tier weight to be 0, got %d", explorerWeight)
	}
}

func TestProvider_DefaultTierMapping(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
			TierMapping: map[string]string{
				testPriceIDBasic: testTierBasic,
				"*":              testTierExplorer, // Wildcard default
			},
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if provider.GetDefaultTier() != testTierExplorer {
		t.Errorf("Expected default tier %s, got %s", testTierExplorer, provider.GetDefaultTier())
	}
}

// Helper function to create a test Stripe event
func createTestStripeEvent(eventType string, data interface{}) *stripe.Event {
	rawData, _ := json.Marshal(data)
	return &stripe.Event{
		ID:      "evt_test_123",
		Type:    stripe.EventType(eventType),
		Created: time.Now().Unix(),
		Data: &stripe.EventData{
			Raw: rawData,
		},
	}
}

func TestProvider_ProcessWebhookEvent_UnknownEvent(t *testing.T) {
	manager := mockManager(t)
	provider, err := NewProvider(Config{
		Config: billing.Config{
			Manager: manager,
		},
		StripeAPIKey:        testStripeAPIKey,
		StripeWebhookSecret: testStripeWebhookSecret,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Unknown event type should be ignored silently
	event := createTestStripeEvent("unknown.event.type", map[string]interface{}{})
	err = provider.processWebhookEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected unknown event to be ignored, got error: %v", err)
	}
}
