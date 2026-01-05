package fiber

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// Create request
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Verify
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "success" {
		t.Errorf("Expected 'success', got %s", string(body))
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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 429 Too Many Requests
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", resp.StatusCode)
	}
}

func TestMiddleware_MissingAuth(t *testing.T) {
	manager := setupTestManager(t)

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 401 Unauthorized
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp.StatusCode)
	}
}

func TestMiddleware_NoEntitlement(t *testing.T) {
	manager := setupTestManager(t)
	// Don't set up entitlement for user2
	// User should be allowed to use default tier quota

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user2")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 200 OK - users without entitlements can use default tier
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		// Mock auth middleware that sets user ID in context
		c.Locals("UserID", "user_from_ctx")
		return c.Next()
	})
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromContext("UserID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(c *fiber.Ctx, usage *goquota.Usage) error {
			customErrorCalled = true
			return c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
				"error": "custom quota exceeded",
				"used":  usage.Used,
				"limit": usage.Limit,
			})
		},
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("Expected status 402, got %d", resp.StatusCode)
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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnRateLimitExceeded: func(c *fiber.Ctx, retryAfter time.Duration, _ *goquota.RateLimitInfo) error {
			customRateLimitCalled = true
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":       "custom rate limit exceeded",
				"retry_after": retryAfter.Seconds(),
			})
		},
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// Make 6 requests rapidly to exceed rate limit
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/test", http.NoBody)
		req.Header.Set("X-User-ID", "user1")
		_, _ = app.Test(req)
	}

	// 6th request should hit rate limit
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !customRateLimitCalled {
		t.Error("Custom rate limit handler was not called")
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", resp.StatusCode)
	}
}

func TestMiddleware_DailyPeriod(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "free")

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// Make 10 requests (daily limit for free tier)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/test", http.NoBody)
		req.Header.Set("X-User-ID", "user1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i+1, err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: Expected status 200, got %d", i+1, resp.StatusCode)
		}
	}

	// 11th request should fail
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected status 429 after exceeding daily limit, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:                 manager,
		GetUserID:               FromHeader("X-User-ID"),
		GetResource:             FixedResource("api_calls"),
		GetAmount:               FixedAmount(1),
		PeriodType:              goquota.PeriodTypeMonthly,
		QuotaExceededStatusCode: http.StatusPaymentRequired, // Use 402 instead of 429
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 402 Payment Required
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("Expected status 402, got %d", resp.StatusCode)
	}
}

func TestMiddleware_IdempotencyKey(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:           manager,
		GetUserID:         FromHeader("X-User-ID"),
		GetResource:       FixedResource("api_calls"),
		GetAmount:         FixedAmount(1),
		PeriodType:        goquota.PeriodTypeMonthly,
		GetIdempotencyKey: IdempotencyKeyFromHeader("X-Request-ID"),
	}))
	app.Post("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// First request
	req := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	req.Header.Set("X-Request-ID", "req-123")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Duplicate request with same idempotency key
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	req2.Header.Set("X-Request-ID", "req-123")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for duplicate request, got %d", resp2.StatusCode)
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

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		// Mock middleware that sets correlation ID
		c.Locals("CorrelationID", "corr-456")
		return c.Next()
	})
	app.Use(Middleware(Config{
		Manager:           manager,
		GetUserID:         FromHeader("X-User-ID"),
		GetResource:       FixedResource("api_calls"),
		GetAmount:         FixedAmount(1),
		PeriodType:        goquota.PeriodTypeMonthly,
		GetIdempotencyKey: IdempotencyKeyFromContext("CorrelationID"),
	}))
	app.Post("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// First request
	req := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Duplicate request
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Verify both requests succeeded (idempotency prevents double-charging)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for duplicate request, got %d", resp2.StatusCode)
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

func TestMiddleware_DynamicCost(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount: DynamicCost(func(c *fiber.Ctx) int {
			if c.Method() == "POST" {
				return 5 // POST costs 5
			}
			return 1 // GET costs 1
		}),
		PeriodType: goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})
	app.Post("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// GET request
	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET: Expected status 200, got %d", resp.StatusCode)
	}

	// POST request
	req2 := httptest.NewRequest("POST", "/api/test", http.NoBody)
	req2.Header.Set("X-User-ID", "user1")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("POST: Expected status 200, got %d", resp2.StatusCode)
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

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(0),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 400 Bad Request for zero amount
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for zero amount, got %d", resp.StatusCode)
	}
}

func TestMiddleware_NegativeAmount(t *testing.T) {
	manager := setupTestManager(t)
	setupEntitlement(t, manager, "user1", "pro")

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(-5),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/api/test", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	req := httptest.NewRequest("GET", "/api/test", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 400 Bad Request for negative amount
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for negative amount, got %d", resp.StatusCode)
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

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("requests"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
	}))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// 80th request - should trigger warning header
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Check headers
	threshold := resp.Header.Get("X-Quota-Warning-Threshold")
	if threshold != "0.80" {
		t.Errorf("Expected X-Quota-Warning-Threshold: 0.80, got %s", threshold)
	}

	used := resp.Header.Get("X-Quota-Warning-Used")
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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("requests"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnWarning: func(c *fiber.Ctx, _ *goquota.Usage, _ float64) {
			customWarningCalled = true
			c.Set("X-Custom-Warning", "true")
		},
	}))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("success")
	})

	// 80th request - should trigger custom warning
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !customWarningCalled {
		t.Error("Custom warning handler was not called")
	}
	if resp.Header.Get("X-Custom-Warning") != "true" {
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

	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		// Verify default OnError behavior (should return 500)
	}))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Should return 500 Internal Server Error
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", resp.StatusCode)
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
	app := fiber.New()
	app.Use(Middleware(Config{
		Manager:     manager,
		GetUserID:   FromHeader("X-User-ID"),
		GetResource: FixedResource("api_calls"),
		GetAmount:   FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnError: func(c *fiber.Ctx, err error) error {
			customErrorCalled = true
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error":   "Service temporarily unavailable",
				"details": err.Error(),
			})
		},
	}))
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-User-ID", "user1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if !customErrorCalled {
		t.Error("Custom error handler was not called")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected 503, got %d", resp.StatusCode)
	}
}

func TestMiddleware_ConfigValidation_MissingManager(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when Manager is nil")
		} else if msg, ok := r.(string); !ok || msg != "goquota/fiber: Config.Manager is required" {
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
		} else if msg, ok := r.(string); !ok || msg != "goquota/fiber: Config.GetUserID is required" {
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
		} else if msg, ok := r.(string); !ok || msg != "goquota/fiber: Config.GetResource is required" {
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
		} else if msg, ok := r.(string); !ok || msg != "goquota/fiber: Config.GetAmount is required" {
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
