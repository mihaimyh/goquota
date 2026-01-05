package echo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// errorStorage is a mock storage that always fails on ConsumeQuota
type errorStorage struct {
	*memory.Storage
}

func (s *errorStorage) ConsumeQuota(_ context.Context, _ *goquota.ConsumeRequest) (int, error) {
	return 0, errors.New("connection refused")
}

// Test helper to create a test manager
func setupTestManager(t *testing.T) *goquota.Manager {
	t.Helper()

	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
				DailyQuotas:   map[string]int{"api_calls": 10},
			},
			"pro": {
				Name:          "pro",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
				DailyQuotas:   map[string]int{"api_calls": 1000},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
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
		SubscriptionStartDate: time.Now().UTC().Add(-1 * time.Hour), // Backdate to ensure period calculations work correctly
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
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// Create request
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	// Execute
	e.ServeHTTP(rec, req)

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
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 429 Too Many Requests
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rec.Code)
	}
}

func TestMiddleware_MissingAuth(t *testing.T) {
	manager := setupTestManager(t)

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	// No X-User-ID header
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 401 Unauthorized
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rec.Code)
	}
}

func TestMiddleware_NoEntitlement(t *testing.T) {
	manager := setupTestManager(t)
	// Don't set up entitlement for user2
	// User should be allowed to use default tier quota

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user2")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

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

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Mock auth middleware that sets user ID in context
			c.Set("UserID", "user_from_ctx")
			return next(c)
		}
	})
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromContext("UserID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify quota was consumed
	ctx := context.Background()
	quota, err := manager.GetQuota(ctx, "user_from_ctx", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 1 {
		t.Errorf("Expected 1 used, got %d", quota.Used)
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
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(c echo.Context, usage *goquota.Usage) error {
			customErrorCalled = true
			return c.JSON(http.StatusPaymentRequired, map[string]interface{}{
				"error": "custom quota exceeded",
				"used":  usage.Used,
				"limit": usage.Limit,
			})
		},
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("Expected status 402, got %d", rec.Code)
	}
}

func TestMiddleware_CustomRateLimitHandler(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      5,
						Window:    time.Second,
						Burst:     5,
					},
				},
			},
		},
	}
	manager, _ := goquota.NewManager(storage, &config)
	setupEntitlement(t, manager, "user1", "free")

	customRateLimitCalled := false
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnRateLimitExceeded: func(c echo.Context, retryAfter time.Duration, _ *goquota.RateLimitInfo) error {
			customRateLimitCalled = true
			return c.JSON(http.StatusTooManyRequests, map[string]interface{}{
				"error":       "custom rate limit exceeded",
				"retry_after": retryAfter.Seconds(),
			})
		},
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// Make 6 requests rapidly to exceed rate limit
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/test", http.NoBody)
		req.Header.Set("X-User-ID", "user1")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
	}

	// 6th request should hit rate limit
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if !customRateLimitCalled {
		t.Error("Custom rate limit handler was not called")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", rec.Code)
	}
}

func TestMiddleware_DailyPeriod(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// Make 10 requests (daily limit for free tier)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/test", http.NoBody)
		req.Header.Set("X-User-ID", "user1")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: Expected status 200, got %d", i+1, rec.Code)
		}
	}

	// 11th request should fail
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 after exceeding daily limit, got %d", rec.Code)
	}
}

func TestMiddleware_ConfigurableStatusCode(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	// Consume quota
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_, _ = manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
	}

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:                 manager,
		GetUserID:               FromHeader("X-User-ID"),
		GetResource:             FixedResource("api_calls"),
		GetAmount:               FixedAmount(1),
		PeriodType:              goquota.PeriodTypeMonthly,
		QuotaExceededStatusCode: http.StatusPaymentRequired, // Use 402 instead of 429
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 402 Payment Required
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("Expected status 402, got %d", rec.Code)
	}
}

func TestMiddleware_IdempotencyKey(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:           manager,
		GetUserID:         FromHeader("X-User-ID"),
		GetResource:       FixedResource("api_calls"),
		GetAmount:         FixedAmount(1),
		PeriodType:        goquota.PeriodTypeMonthly,
		GetIdempotencyKey: IdempotencyKeyFromHeader("X-Request-ID"),
	}))
	e.POST("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// First request
	req := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	req.Header.Set("X-Request-ID", "req-123")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Duplicate request with same idempotency key
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	req2.Header.Set("X-Request-ID", "req-123")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("Expected status 200 for duplicate request, got %d", rec2.Code)
	}

	// Verify quota was only consumed once
	ctx := context.Background()
	quota, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 1 {
		t.Errorf("Expected 1 used (idempotent), got %d", quota.Used)
	}
}

func TestMiddleware_CustomIdempotencyKeyExtractor(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Mock middleware that sets correlation ID
			c.Set("CorrelationID", "corr-456")
			return next(c)
		}
	})
	e.Use(Middleware(Config{
		Manager:           manager,
		GetUserID:         FromHeader("X-User-ID"),
		GetResource:       FixedResource("api_calls"),
		GetAmount:         FixedAmount(1),
		PeriodType:        goquota.PeriodTypeMonthly,
		GetIdempotencyKey: IdempotencyKeyFromContext("CorrelationID"),
	}))
	e.POST("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// First request
	req := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Duplicate request
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	// Verify quota was only consumed once
	ctx := context.Background()
	quota, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 1 {
		t.Errorf("Expected 1 used (idempotent), got %d", quota.Used)
	}
}

func TestMiddleware_DynamicCost(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount: DynamicCost(func(c echo.Context) int {
			if c.Request().Method == "POST" {
				return 5 // POST costs 5
			}
			return 1 // GET costs 1
		}),
		PeriodType: goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})
	e.POST("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// GET request
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET: Expected status 200, got %d", rec.Code)
	}

	// POST request
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("POST: Expected status 200, got %d", rec2.Code)
	}

	// Verify quota: 1 (GET) + 5 (POST) = 6
	ctx := context.Background()
	quota, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Failed to get quota: %v", err)
	}
	if quota.Used != 6 {
		t.Errorf("Expected 6 used (1 + 5), got %d", quota.Used)
	}
}

func TestMiddleware_ZeroAmount(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(0),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 400 Bad Request for zero amount
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for zero amount, got %d", rec.Code)
	}
}

func TestMiddleware_NegativeAmount(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(-5),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/api/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 400 Bad Request for negative amount
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for negative amount, got %d", rec.Code)
	}
}

func TestMiddleware_Warnings(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"requests": 100},
				WarningThresholds: map[string][]float64{
					"requests": {0.8},
				},
			},
		},
	}
	manager, _ := goquota.NewManager(storage, &config)
	setupEntitlement(t, manager, "user1", "free")

	// Consume up to 79%
	ctx := context.Background()
	for i := 0; i < 79; i++ {
		_, _ = manager.Consume(ctx, "user1", "requests", 1, goquota.PeriodTypeMonthly)
	}

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("requests"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// 80th request - should trigger warning header
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

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

func TestMiddleware_CustomWarningHandler(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"requests": 100},
				WarningThresholds: map[string][]float64{
					"requests": {0.8},
				},
			},
		},
	}
	manager, _ := goquota.NewManager(storage, &config)
	setupEntitlement(t, manager, "user1", "free")

	// Consume up to 79%
	ctx := context.Background()
	for i := 0; i < 79; i++ {
		_, _ = manager.Consume(ctx, "user1", "requests", 1, goquota.PeriodTypeMonthly)
	}

	customWarningCalled := false
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("requests"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnWarning: func(c echo.Context, _ *goquota.Usage, _ float64) {
			customWarningCalled = true
			c.Response().Header().Set("X-Custom-Warning", "true")
		},
	}))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// 80th request - should trigger custom warning
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if !customWarningCalled {
		t.Error("Custom warning handler was not called")
	}
	if rec.Header().Get("X-Custom-Warning") != "true" {
		t.Error("Custom warning header was not set")
	}
}

func TestMiddleware_StorageError(t *testing.T) {
	// Setup manager with failing storage
	storage := &errorStorage{memory.New()}
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
			},
		},
	}
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		// Verify default OnError behavior (should return 500)
	}))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	// Should return 500 Internal Server Error
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rec.Code)
	}
}

func TestMiddleware_StorageError_CustomHandler(t *testing.T) {
	// Setup manager with failing storage
	storage := &errorStorage{memory.New()}
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
			},
		},
	}
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	customErrorCalled := false
	e := echo.New()
	e.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnError: func(c echo.Context, err error) error {
			customErrorCalled = true
			return c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
				"error":   "Service temporarily unavailable",
				"details": err.Error(),
			})
		},
	}))
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503, got %d", rec.Code)
	}
}

func TestMiddleware_ConfigValidation_MissingManager(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when Manager is nil")
		} else if msg, ok := r.(string); !ok || msg != "goquota/echo: Config.Manager is required" {
			t.Errorf("Expected panic message about Manager, got: %v", r)
		}
	}()

	_ = Middleware(Config{
		// Manager is nil
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
	})
}

func TestMiddleware_ConfigValidation_MissingGetUserID(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when GetUserID is nil")
		} else if msg, ok := r.(string); !ok || msg != "goquota/echo: Config.GetUserID is required" {
			t.Errorf("Expected panic message about GetUserID, got: %v", r)
		}
	}()

	manager := setupTestManager(t)
	_ = Middleware(Config{
		Manager: manager,
		// GetUserID is nil
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
	})
}

func TestMiddleware_ConfigValidation_MissingGetResource(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when GetResource is nil")
		} else if msg, ok := r.(string); !ok || msg != "goquota/echo: Config.GetResource is required" {
			t.Errorf("Expected panic message about GetResource, got: %v", r)
		}
	}()

	manager := setupTestManager(t)
	_ = Middleware(Config{
		Manager:   manager,
		GetUserID: FromHeader("X-User-ID"),
		// GetResource is nil
		GetAmount: FixedAmount(1),
	})
}

func TestMiddleware_ConfigValidation_MissingGetAmount(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when GetAmount is nil")
		} else if msg, ok := r.(string); !ok || msg != "goquota/echo: Config.GetAmount is required" {
			t.Errorf("Expected panic message about GetAmount, got: %v", r)
		}
	}()

	manager := setupTestManager(t)
	_ = Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		// GetAmount is nil
	})
}
