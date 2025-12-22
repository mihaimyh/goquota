// Package main demonstrates goquota integration with Gin framework
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	httpMiddleware "github.com/mihaimyh/goquota/middleware/http"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// ginQuotaMiddleware adapts goquota middleware for Gin
func ginQuotaMiddleware(config httpMiddleware.Config) gin.HandlerFunc {
	middleware := httpMiddleware.Middleware(&config)

	return func(c *gin.Context) {
		// Wrap Gin context in standard http.Handler
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Next()
		})).ServeHTTP(c.Writer, c.Request)

		// If middleware aborted (quota exceeded), stop Gin chain
		if c.Writer.Status() >= 400 {
			c.Abort()
		}
	}
}

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

	// Public routes
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

	// Protected routes with quota
	api := r.Group("/api")
	api.Use(ginQuotaMiddleware(httpMiddleware.Config{
		Manager:     manager,
		GetUserID:   httpMiddleware.FromHeader("X-User-ID"),
		GetResource: httpMiddleware.FixedResource("api_calls"),
		GetAmount:   httpMiddleware.FixedAmount(1),
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

	// Start server
	println("Gin server starting on :8080")
	println("Try: curl -H \"X-User-ID: user1\" http://localhost:8080/api/data")

	if err := r.Run(":8080"); err != nil {
		panic(err)
	}
}
