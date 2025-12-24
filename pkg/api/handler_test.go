package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

const (
	testUserID   = "user123"
	testUserID2  = "test-user"
	testResource = "api_calls"
)

// Helper to create a test manager
func newTestManager() *goquota.Manager {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
			"pro": {
				Name: "pro",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
					"gpt4":      50,
				},
				InitialForeverCredits: map[string]int{
					"api_calls": 500,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeMonthly,
					goquota.PeriodTypeForever,
				},
			},
			"unlimited": {
				Name: "unlimited",
				MonthlyQuotas: map[string]int{
					"api_calls": -1, // Unlimited
				},
			},
		},
	}
	manager, _ := goquota.NewManager(storage, config)
	return manager
}

func TestHandler_GetUsage_HappyPath_MonthlyOnly(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Consume some quota
	_, _ = manager.Consume(ctx, userID, "api_calls", 25, goquota.PeriodTypeMonthly)

	// Create handler with KnownResources
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{testResource},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.UserID != userID {
		t.Errorf("Expected userID %s, got %s", userID, response.UserID)
	}
	if response.Tier != "free" {
		t.Errorf("Expected tier 'free', got %s", response.Tier)
	}
	if response.Status != statusActive {
		t.Errorf("Expected status 'active', got %s", response.Status)
	}

	resourceUsage, ok := response.Resources["api_calls"]
	if !ok {
		t.Fatal("Expected 'api_calls' resource in response")
	}

	if resourceUsage.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", resourceUsage.Limit)
	}
	if resourceUsage.Used != 25 {
		t.Errorf("Expected used 25, got %d", resourceUsage.Used)
	}
	if resourceUsage.Remaining != 75 {
		t.Errorf("Expected remaining 75, got %d", resourceUsage.Remaining)
	}
	if resourceUsage.ResetAt == nil {
		t.Error("Expected reset_at to be set")
	}

	// Check breakdown
	if len(resourceUsage.Breakdown) != 1 {
		t.Fatalf("Expected 1 breakdown item, got %d", len(resourceUsage.Breakdown))
	}
	if resourceUsage.Breakdown[0].Source != sourceMonthly {
		t.Errorf("Expected source 'monthly', got %s", resourceUsage.Breakdown[0].Source)
	}
}

func TestHandler_GetUsage_HappyPath_MonthlyAndForever(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Note: InitialForeverCredits (500) is automatically applied when entitlement is set
	// Top up additional forever credits
	_ = manager.TopUpLimit(ctx, userID, "api_calls", 500, goquota.WithTopUpIdempotencyKey("topup1"))

	// Consume from monthly
	_, _ = manager.Consume(ctx, userID, "api_calls", 150, goquota.PeriodTypeMonthly)

	// Create handler with KnownResources
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls", "gpt4"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	resourceUsage, ok := response.Resources["api_calls"]
	if !ok {
		t.Fatal("Expected 'api_calls' resource in response")
	}

	// Combined: 1000 (monthly) + 1000 (forever: 500 initial + 500 topup) = 2000
	expectedLimit := 2000
	if resourceUsage.Limit != expectedLimit {
		t.Errorf("Expected limit %d, got %d", expectedLimit, resourceUsage.Limit)
	}
	// Used: 150 (from monthly)
	if resourceUsage.Used != 150 {
		t.Errorf("Expected used 150, got %d", resourceUsage.Used)
	}
	expectedRemaining := 1850
	if resourceUsage.Remaining != expectedRemaining {
		t.Errorf("Expected remaining %d, got %d", expectedRemaining, resourceUsage.Remaining)
	}

	// Check breakdown has both monthly and forever
	if len(resourceUsage.Breakdown) != 2 {
		t.Fatalf("Expected 2 breakdown items, got %d", len(resourceUsage.Breakdown))
	}

	// First should be monthly (based on ConsumptionOrder)
	if resourceUsage.Breakdown[0].Source != sourceMonthly {
		t.Errorf("Expected first breakdown source 'monthly', got %s", resourceUsage.Breakdown[0].Source)
	}
	if resourceUsage.Breakdown[0].Limit != 1000 {
		t.Errorf("Expected monthly limit 1000, got %d", resourceUsage.Breakdown[0].Limit)
	}
	if resourceUsage.Breakdown[0].Used != 150 {
		t.Errorf("Expected monthly used 150, got %d", resourceUsage.Breakdown[0].Used)
	}

	// Second should be forever
	if resourceUsage.Breakdown[1].Source != sourceForever {
		t.Errorf("Expected second breakdown source 'forever', got %s", resourceUsage.Breakdown[1].Source)
	}
	// Forever balance: 1000 - 0 = 1000 (500 initial + 500 topup, not consumed yet)
	if resourceUsage.Breakdown[1].Balance != 1000 {
		t.Errorf("Expected forever balance 1000, got %d", resourceUsage.Breakdown[1].Balance)
	}
}

func TestHandler_GetUsage_Unlimited_MonthlyUnlimited(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement to unlimited tier
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "unlimited",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Top up forever credits
	_ = manager.TopUpLimit(ctx, userID, "api_calls", 50, goquota.WithTopUpIdempotencyKey("topup1"))

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	resourceUsage, ok := response.Resources["api_calls"]
	if !ok {
		t.Fatal("Expected 'api_calls' resource in response")
	}

	// CRITICAL: If monthly is unlimited, combined should be unlimited
	if resourceUsage.Limit != -1 {
		t.Errorf("Expected limit -1 (unlimited), got %d", resourceUsage.Limit)
	}
	if resourceUsage.Remaining != -1 {
		t.Errorf("Expected remaining -1 (unlimited), got %d", resourceUsage.Remaining)
	}
}

func TestHandler_GetUsage_OrphanedCredits(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// User starts with pro tier and buys credits
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Buy credits for a resource
	_ = manager.TopUpLimit(ctx, userID, "orphaned_resource", 1000, goquota.WithTopUpIdempotencyKey("topup1"))

	// Downgrade to free tier (which doesn't have orphaned_resource)
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Create handler with KnownResources including orphaned resource
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls", "orphaned_resource"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should include orphaned_resource even though it's not in free tier
	if _, ok := response.Resources["orphaned_resource"]; !ok {
		t.Error("Expected 'orphaned_resource' to be included (orphaned credits)")
	}

	orphanedUsage := response.Resources["orphaned_resource"]
	// Should show forever credits
	if orphanedUsage.Limit != 1000 {
		t.Errorf("Expected orphaned resource limit 1000, got %d", orphanedUsage.Limit)
	}
}

func TestHandler_GetUsage_ResourceFilter(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Create handler with ResourceFilter
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls", "gpt4"},
		ResourceFilter: func(resources []string) []string {
			// Only return api_calls
			filtered := make([]string, 0)
			for _, r := range resources {
				if r == "api_calls" {
					filtered = append(filtered, r)
				}
			}
			return filtered
		},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should only have api_calls, not gpt4
	if _, ok := response.Resources["api_calls"]; !ok {
		t.Error("Expected 'api_calls' resource in response")
	}
	if _, ok := response.Resources["gpt4"]; ok {
		t.Error("Expected 'gpt4' resource to be filtered out")
	}
}

func TestHandler_GetUsage_NoEntitlement(t *testing.T) {
	manager := newTestManager()
	userID := testUserID

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != statusDefault {
		t.Errorf("Expected status 'default', got %s", response.Status)
	}
	if response.Tier != tierDefault {
		t.Errorf("Expected tier 'default', got %s", response.Tier)
	}
}

func TestHandler_GetUsage_MissingUserID(t *testing.T) {
	manager := newTestManager()

	// Create handler
	handler, err := NewHandler(Config{
		Manager:   manager,
		GetUserID: func(_ *http.Request) string { return "" }, // Empty user ID
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Should return 401
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_GetUsage_ExpiredEntitlement(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set expired entitlement
	expiredTime := time.Now().UTC().Add(-24 * time.Hour)
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		ExpiresAt:             &expiredTime,
		UpdatedAt:             time.Now().UTC(),
	})

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != statusExpired {
		t.Errorf("Expected status 'expired', got %s", response.Status)
	}
}

func TestNewHandler_InvalidConfig(t *testing.T) {
	// Test nil manager
	_, err := NewHandler(Config{
		Manager:   nil,
		GetUserID: func(_ *http.Request) string { return "user" },
	})
	if err == nil {
		t.Error("Expected error for nil manager")
	}

	// Test nil GetUserID
	manager := newTestManager()
	_, err = NewHandler(Config{
		Manager:   manager,
		GetUserID: nil,
	})
	if err == nil {
		t.Error("Expected error for nil GetUserID")
	}
}

func TestFromHeader(t *testing.T) {
	extractor := FromHeader("X-User-ID")
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", testUserID2)

	userID := extractor(req)
	if userID != testUserID2 {
		t.Errorf("Expected 'test-user', got %s", userID)
	}
}

func TestFromContext(t *testing.T) {
	type contextKey string
	key := contextKey("userID")
	extractor := FromContext(key)

	req := httptest.NewRequest("GET", "/", http.NoBody)
	ctx := context.WithValue(req.Context(), key, testUserID2)
	req = req.WithContext(ctx)

	userID := extractor(req)
	if userID != testUserID2 {
		t.Errorf("Expected 'test-user', got %s", userID)
	}
}

func TestHandler_GetUsage_ZeroLimits(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement to free tier (which has api_calls with 0 monthly quota)
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	resourceUsage, ok := response.Resources["api_calls"]
	if !ok {
		t.Fatal("Expected 'api_calls' resource in response")
	}

	// Should handle zero limits gracefully
	if resourceUsage.Limit < 0 {
		t.Errorf("Expected limit >= 0, got %d", resourceUsage.Limit)
	}
}

func TestHandler_GetUsage_ExceededQuota(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Consume more than limit (should fail, but usage will be at limit)
	_, _ = manager.Consume(ctx, userID, "api_calls", 150, goquota.PeriodTypeMonthly)

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"api_calls"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	resourceUsage, ok := response.Resources["api_calls"]
	if !ok {
		t.Fatal("Expected 'api_calls' resource in response")
	}

	// Should handle exceeded quota gracefully (used may be >= limit)
	if resourceUsage.Remaining < 0 {
		t.Errorf("Expected remaining >= 0, got %d", resourceUsage.Remaining)
	}
}

func TestHandler_GetUsage_ForeverOnly(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// Set entitlement to free tier
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Top up forever credits for a resource that doesn't have monthly quota in free tier
	// Use "orphaned_resource" which doesn't exist in free tier config
	_ = manager.TopUpLimit(ctx, userID, "orphaned_resource", 500, goquota.WithTopUpIdempotencyKey("topup1"))

	// Create handler
	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{"orphaned_resource"},
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response UsageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	resourceUsage, ok := response.Resources["orphaned_resource"]
	if !ok {
		t.Fatal("Expected 'orphaned_resource' resource in response")
	}

	// Should show forever credits only (no monthly quota)
	if resourceUsage.Limit != 500 {
		t.Errorf("Expected limit 500, got %d", resourceUsage.Limit)
	}

	// Breakdown should only have forever (monthly will have 0 limit and 0 used, so not included)
	// Actually, if monthly has 0 limit and 0 used, it might still be included
	// Let's check that forever is present
	hasForever := false
	for _, bd := range resourceUsage.Breakdown {
		if bd.Source == sourceForever {
			hasForever = true
			if bd.Balance != 500 {
				t.Errorf("Expected forever balance 500, got %d", bd.Balance)
			}
		}
	}
	if !hasForever {
		t.Error("Expected 'forever' in breakdown")
	}
}

func TestHandler_GetUsage_InvalidUserID(t *testing.T) {
	manager := newTestManager()

	// Create handler
	handler, err := NewHandler(Config{
		Manager:   manager,
		GetUserID: func(_ *http.Request) string { return "a" + string(make([]byte, 300)) }, // Too long
	})
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Create request
	req := httptest.NewRequest("GET", "/usage", http.NoBody)
	w := httptest.NewRecorder()

	// Call handler
	handler.GetUsage(w, req)

	// Should return 400 Bad Request
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}
