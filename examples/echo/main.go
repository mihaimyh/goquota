// Package main demonstrates goquota integration with Echo framework
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	echoMiddleware "github.com/mihaimyh/goquota/middleware/echo"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// Create quota manager
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				DailyQuotas: map[string]int{"api_calls": 10},
			},
			"pro": {
				DailyQuotas: map[string]int{"api_calls": 1000},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		panic(err)
	}

	// Set up test users
	ctx := context.Background()
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Create Echo instance
	e := echo.New()

	// Mock authentication middleware (sets UserID in context)
	// In production, this would validate JWT tokens, sessions, etc.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Extract user ID from header (in production, extract from JWT/session)
			userID := c.Request().Header.Get("X-User-ID")
			if userID != "" {
				c.Set("UserID", userID) // Set in context for quota middleware
			}
			return next(c)
		}
	})

	// Public routes (no quota enforcement)
	e.GET("/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	e.GET("/quota", func(c echo.Context) error {
		userID := c.Request().Header.Get("X-User-ID")
		if userID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Missing X-User-ID header"})
		}

		usage, err := manager.GetQuota(c.Request().Context(), userID, "api_calls", goquota.PeriodTypeDaily)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"used":      usage.Used,
			"limit":     usage.Limit,
			"remaining": usage.Limit - usage.Used,
		})
	})

	// Protected routes with quota enforcement
	api := e.Group("/api")
	// Use native Echo middleware - extracts UserID from context set by auth middleware
	api.Use(echoMiddleware.Middleware(echoMiddleware.Config{
		Manager:     manager,
		GetUserID:   echoMiddleware.FromContext("UserID"), // Recommended: Extract from context (set by auth middleware)
		GetResource: echoMiddleware.FixedResource("api_calls"),
		GetAmount:   echoMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	api.GET("/data", func(c echo.Context) error {
		return c.String(http.StatusOK, "Data retrieved successfully")
	})
	api.POST("/process", func(c echo.Context) error {
		return c.String(http.StatusOK, "Processing complete")
	})

	// Alternative example: Header-based extraction (for simple cases)
	apiAlt := e.Group("/api-alt")
	apiAlt.Use(echoMiddleware.Middleware(echoMiddleware.Config{
		Manager:     manager,
		GetUserID:   echoMiddleware.FromHeader("X-User-ID"), // Alternative: Extract directly from header
		GetResource: echoMiddleware.FixedResource("api_calls"),
		GetAmount:   echoMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	apiAlt.GET("/data", func(c echo.Context) error {
		return c.String(http.StatusOK, "Data retrieved successfully (header-based)")
	})

	// Example with dynamic cost: POST requests cost more
	apiDynamic := e.Group("/api-dynamic")
	apiDynamic.Use(echoMiddleware.Middleware(echoMiddleware.Config{
		Manager:     manager,
		GetUserID:   echoMiddleware.FromContext("UserID"),
		GetResource: echoMiddleware.FixedResource("api_calls"),
		GetAmount: echoMiddleware.DynamicCost(func(c echo.Context) int {
			// POST requests cost 5, GET requests cost 1
			if c.Request().Method == "POST" {
				return 5
			}
			return 1
		}),
		PeriodType: goquota.PeriodTypeDaily,
	}))
	apiDynamic.GET("/read", func(c echo.Context) error {
		return c.String(http.StatusOK, "Read operation (cost: 1)")
	})
	apiDynamic.POST("/write", func(c echo.Context) error {
		return c.String(http.StatusOK, "Write operation (cost: 5)")
	})

	// Example with custom error handler
	apiCustom := e.Group("/api-custom")
	apiCustom.Use(echoMiddleware.Middleware(echoMiddleware.Config{
		Manager:                 manager,
		GetUserID:               echoMiddleware.FromContext("UserID"),
		GetResource:             echoMiddleware.FixedResource("api_calls"),
		GetAmount:               echoMiddleware.FixedAmount(1),
		PeriodType:              goquota.PeriodTypeDaily,
		QuotaExceededStatusCode: http.StatusPaymentRequired, // Use 402 instead of 429
		OnQuotaExceeded: func(c echo.Context, usage *goquota.Usage) error {
			// Custom error response format
			return c.JSON(http.StatusPaymentRequired, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "QUOTA_EXCEEDED",
					"message": "Monthly quota exceeded",
					"details": map[string]interface{}{
						"used":  usage.Used,
						"limit": usage.Limit,
					},
				},
			})
		},
	}))
	apiCustom.GET("/premium", func(c echo.Context) error {
		return c.String(http.StatusOK, "Premium endpoint")
	})

	// Start server
	println("Echo server starting on :8080")
	println("Try: curl -H \"X-User-ID: user1\" http://localhost:8080/api/data")
	println("Check quota: curl -H \"X-User-ID: user1\" http://localhost:8080/quota")

	if err := e.Start(":8080"); err != nil {
		panic(err)
	}
}

