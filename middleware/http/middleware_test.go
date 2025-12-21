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
		w.Write([]byte("success"))
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
		_ = manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
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

	// Should return 403 Forbidden (no entitlement)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", rec.Code)
	}
}

func TestMiddleware_FromContext(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user_from_ctx", "pro")

	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromContext("user_id"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	})

	// Wrap with middleware that sets context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), "user_id", "user_from_ctx")
		r = r.WithContext(ctx)

		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
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
		_ = manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
	}

	customErrorCalled := false
	mw := Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(w http.ResponseWriter, r *http.Request, quota *goquota.Quota) {
			customErrorCalled = true
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte("custom quota exceeded"))
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
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("Expected status 402, got %d", rec.Code)
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
