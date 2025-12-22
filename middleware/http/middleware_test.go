package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// Test helper to create a test manager
func setupTestManager(t *testing.T) *goquota.Manager {
	t.Helper()

	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				MonthlyQuotas: map[string]int{"api_calls": 100},
				DailyQuotas:   map[string]int{"api_calls": 10},
			},
			"pro": {
				MonthlyQuotas: map[string]int{"api_calls": 10000},
				DailyQuotas:   map[string]int{"api_calls": 1000},
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	return manager
}

// Test helper to set up entitlement
func setupEntitlement(t *testing.T, manager *goquota.Manager, userID, tier string) {
	t.Helper()

	ctx := context.Background()
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Failed to set entitlement: %v", err)
	}
}

func TestMiddleware_Success(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	// Create middleware
	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	// Create test handler
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("success")); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	// Create request
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	// Execute
	handler.ServeHTTP(rec, req)

	// Verify
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "success" {
		t.Errorf("Expected 'success', got %s", rec.Body.String())
	}
}

func TestMiddleware_QuotaExceeded(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	// Consume quota up to limit
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_, _ = manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
	}

	// Create middleware
	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 429 Too Many Requests
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rec.Code)
	}
}

func TestMiddleware_MissingAuth(t *testing.T) {
	manager := setupTestManager(t)

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	// No X-User-ID header
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401 Unauthorized
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}
}

func TestMiddleware_NoEntitlement(t *testing.T) {
	manager := setupTestManager(t)
	// Don't set up entitlement for user2
	// User should be allowed to use default tier quota

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user2")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 200 OK - users without entitlements can use default tier
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify quota was consumed using default tier
	ctx := context.Background()
	quota, err := manager.GetQuota(ctx, "user2", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 1 {
		t.Errorf("Expected 1 used, got %d", quota.Used)
	}
	// Default tier is "free" with 100 monthly quota
	if quota.Limit != 100 {
		t.Errorf("Expected limit 100 (default tier), got %d", quota.Limit)
	}
}

func TestMiddleware_FromContext(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user_from_ctx", "pro")

	key := ContextKey("user_id")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromContext(key),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create request with context value already set
	req := httptest.NewRequest("GET", "/api/test", nil)
	ctx := context.WithValue(req.Context(), key, "user_from_ctx")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify quota was consumed
	ctxBg := context.Background()
	quota, err := manager.GetQuota(ctxBg, "user_from_ctx", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 1 {
		t.Errorf("Expected 1 used, got %d", quota.Used)
	}
}

func TestMiddleware_BodyLength(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   BodyLength(),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body to verify it's still available
		body, _ := io.ReadAll(r.Body)
		if string(body) != "test payload" {
			t.Errorf("Body not preserved: got %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))

	body := bytes.NewBufferString("test payload")
	req := httptest.NewRequest("POST", "/api/test", body)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify quota was consumed (12 bytes for "test payload")
	ctx := context.Background()
	quota, _ := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if quota.Used != 12 {
		t.Errorf("Expected 12 bytes consumed, got %d", quota.Used)
	}
}

func TestMiddleware_CustomErrorHandler(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	// Consume quota
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_, _ = manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
	}

	customErrorCalled := false
	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(w http.ResponseWriter, r *http.Request, usage *goquota.Usage) {
			customErrorCalled = true
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("custom quota exceeded"))
		},
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rec.Code)
	}
	if rec.Body.String() != "custom quota exceeded" {
		t.Errorf("Expected custom message, got %s", rec.Body.String())
	}
}

func TestMiddleware_DailyPeriod(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make 10 requests (daily limit for free tier)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-User-ID", "user1")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: Expected status 200, got %d", i+1, rec.Code)
		}
	}

	// 11th request should fail
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 after exceeding daily limit, got %d", rec.Code)
	}
}

func TestMiddleware_ConcurrentRequests(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make 100 concurrent requests
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("X-User-ID", "user1")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", rec.Code)
			}
			done <- true
		}()
	}

	// Wait for all requests
	for i := 0; i < 100; i++ {
		<-done
	}

	// Verify quota
	ctx := context.Background()
	quota, _ := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if quota.Used != 100 {
		t.Errorf("Expected 100 used, got %d", quota.Used)
	}
}

func TestMiddleware_ZeroAmount(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(0),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 400 Bad Request for zero amount
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for zero amount, got %d", rec.Code)
	}
}

func TestMiddleware_NegativeAmount(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(-5),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 400 Bad Request for negative amount
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for negative amount, got %d", rec.Code)
	}
}

func TestMiddleware_EmptyBody(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   BodyLength(),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/test", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Empty body should be treated as 0 bytes, which is invalid
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for empty body, got %d", rec.Code)
	}
}

func TestMiddleware_Warnings(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				MonthlyQuotas: map[string]int{"requests": 100},
				WarningThresholds: map[string][]float64{
					"requests": {0.8},
				},
			},
		},
	}
	manager, _ := goquota.NewManager(storage, config)
	setupEntitlement(t, manager, "user1", "free")

	// Consume up to 79%
	ctx := context.Background()
	for i := 0; i < 79; i++ {
		_, _ = manager.Consume(ctx, "user1", "requests", 1, goquota.PeriodTypeMonthly)
	}

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("requests"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 80th request - should trigger warning header
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Check headers
	threshold := rec.Header().Get("X-Quota-Warning-Threshold")
	if threshold != "0.80" {
		t.Errorf("Expected X-Quota-Warning-Threshold: 0.80, got %s", threshold)
	}

	used := rec.Header().Get("X-Quota-Warning-Used")
	if used != "80" {
		t.Errorf("Expected X-Quota-Warning-Used: 80, got %s", used)
	}
}

func TestJSONHelpers(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	tests := []struct {
		name           string
		extractor      AmountExtractor
		payload        string
		expectedAmount int
		expectError    bool
	}{
		{
			name:           "JSONIntField - Success",
			extractor:      JSONIntField("count"),
			payload:        `{"count": 5}`,
			expectedAmount: 5,
		},
		{
			name:           "JSONIntField - Float",
			extractor:      JSONIntField("count"),
			payload:        `{"count": 5.5}`,
			expectedAmount: 5,
		},
		{
			name:        "JSONIntField - Missing",
			extractor:   JSONIntField("missing"),
			payload:     `{"count": 5}`,
			expectError: true,
		},
		{
			name:           "JSONDurationMillisToSeconds - Round Up",
			extractor:      JSONDurationMillisToSeconds("duration"),
			payload:        `{"duration": 1500}`,
			expectedAmount: 2,
		},
		{
			name:           "JSONDurationMillisToSeconds - Exact",
			extractor:      JSONDurationMillisToSeconds("duration"),
			payload:        `{"duration": 2000}`,
			expectedAmount: 2,
		},
		{
			name:           "JSONStringByteLength - ASCII",
			extractor:      JSONStringByteLength("text"),
			payload:        `{"text": "hello"}`,
			expectedAmount: 5,
		},
		{
			name:           "JSONStringByteLength - UTF-8",
			extractor:      JSONStringByteLength("text"),
			payload:        `{"text": "世界"}`, // 6 bytes in UTF-8
			expectedAmount: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", bytes.NewBufferString(tt.payload))
			amount, err := tt.extractor(req)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if amount != tt.expectedAmount {
				t.Errorf("Expected amount %d, got %d", tt.expectedAmount, amount)
			}

			// Verify body is preserved
			body, _ := io.ReadAll(req.Body)
			if string(body) != tt.payload {
				t.Errorf("Body not preserved: expected %q, got %q", tt.payload, string(body))
			}
		})
	}
}
