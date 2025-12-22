// Package main demonstrates goquota integration with Gin framework
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	ginMiddleware "github.com/mihaimyh/goquota/middleware/gin"
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

	// Create Gin router
	r := gin.Default()

	// Mock authentication middleware (sets UserID in context)
	// In production, this would validate JWT tokens, sessions, etc.
	r.Use(func(c *gin.Context) {
		// Extract user ID from header (in production, extract from JWT/session)
		userID := c.GetHeader("X-User-ID")
		if userID != "" {
			c.Set("UserID", userID) // Set in context for quota middleware
		}
		c.Next()
	})

	// Public routes (no quota enforcement)
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	r.GET("/quota", func(c *gin.Context) {
		userID := c.GetHeader("X-User-ID")
		if userID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-User-ID header"})
			return
		}

		usage, err := manager.GetQuota(c.Request.Context(), userID, "api_calls", goquota.PeriodTypeDaily)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"used":      usage.Used,
			"limit":     usage.Limit,
			"remaining": usage.Limit - usage.Used,
		})
	})

	// Protected routes with quota enforcement
	api := r.Group("/api")
	// Use native Gin middleware - extracts UserID from context set by auth middleware
	api.Use(ginMiddleware.Middleware(ginMiddleware.Config{
		Manager:     manager,
		GetUserID:   ginMiddleware.FromContext("UserID"), // Recommended: Extract from context (set by auth middleware)
		GetResource: ginMiddleware.FixedResource("api_calls"),
		GetAmount:   ginMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	{
		api.GET("/data", func(c *gin.Context) {
			c.String(http.StatusOK, "Data retrieved successfully")
		})

		api.POST("/process", func(c *gin.Context) {
			c.String(http.StatusOK, "Processing complete")
		})
	}

	// Alternative example: Header-based extraction (for simple cases)
	apiAlt := r.Group("/api-alt")
	apiAlt.Use(ginMiddleware.Middleware(ginMiddleware.Config{
		Manager:     manager,
		GetUserID:   ginMiddleware.FromHeader("X-User-ID"), // Alternative: Extract directly from header
		GetResource: ginMiddleware.FixedResource("api_calls"),
		GetAmount:   ginMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
	}))
	{
		apiAlt.GET("/data", func(c *gin.Context) {
			c.String(http.StatusOK, "Data retrieved successfully (header-based)")
		})
	}

	// Example with dynamic cost: POST requests cost more
	apiDynamic := r.Group("/api-dynamic")
	apiDynamic.Use(ginMiddleware.Middleware(ginMiddleware.Config{
		Manager:     manager,
		GetUserID:   ginMiddleware.FromContext("UserID"),
		GetResource: ginMiddleware.FixedResource("api_calls"),
		GetAmount: ginMiddleware.DynamicCost(func(c *gin.Context) int {
			// POST requests cost 5, GET requests cost 1
			if c.Request.Method == "POST" {
				return 5
			}
			return 1
		}),
		PeriodType: goquota.PeriodTypeDaily,
	}))
	{
		apiDynamic.GET("/read", func(c *gin.Context) {
			c.String(http.StatusOK, "Read operation (cost: 1)")
		})
		apiDynamic.POST("/write", func(c *gin.Context) {
			c.String(http.StatusOK, "Write operation (cost: 5)")
		})
	}

	// Example with custom error handler
	apiCustom := r.Group("/api-custom")
	apiCustom.Use(ginMiddleware.Middleware(ginMiddleware.Config{
		Manager:                 manager,
		GetUserID:               ginMiddleware.FromContext("UserID"),
		GetResource:             ginMiddleware.FixedResource("api_calls"),
		GetAmount:               ginMiddleware.FixedAmount(1),
		PeriodType:              goquota.PeriodTypeDaily,
		QuotaExceededStatusCode: http.StatusPaymentRequired, // Use 402 instead of 429
		OnQuotaExceeded: func(c *gin.Context, usage *goquota.Usage) {
			// Custom error response format
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error": gin.H{
					"code":    "QUOTA_EXCEEDED",
					"message": "Monthly quota exceeded",
					"details": gin.H{
						"used":  usage.Used,
						"limit": usage.Limit,
					},
				},
			})
		},
	}))
	{
		apiCustom.GET("/premium", func(c *gin.Context) {
			c.String(http.StatusOK, "Premium endpoint")
		})
	}

	// Start server
	println("Gin server starting on :8080")
	println("Try: curl -H \"X-User-ID: user1\" http://localhost:8080/api/data")
	println("Check quota: curl -H \"X-User-ID: user1\" http://localhost:8080/quota")

	if err := r.Run(":8080"); err != nil {
		panic(err)
	}
}
