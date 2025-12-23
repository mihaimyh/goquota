// Package main demonstrates all goquota features in a comprehensive example
// including Redis storage, rate limiting, idempotency, refunds, tier changes,
// soft limits, fallback strategies, observability, and HTTP middleware.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	ginMiddleware "github.com/mihaimyh/goquota/middleware/gin"
	"github.com/mihaimyh/goquota/pkg/goquota"
	zerolog_adapter "github.com/mihaimyh/goquota/pkg/goquota/logger/zerolog"
	prometheus_adapter "github.com/mihaimyh/goquota/pkg/goquota/metrics/prometheus"
	"github.com/mihaimyh/goquota/storage/memory"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

func main() {
	ctx := context.Background()

	fmt.Println("=== goquota Comprehensive Example ===")

	// ============================================================
	// 1. Setup Observability (Logging & Metrics)
	// ============================================================
	fmt.Println("1. Setting up observability...")
	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	zlog := zerolog.New(output).With().Timestamp().Logger()
	logger := zerolog_adapter.NewLogger(&zlog)
	metrics := prometheus_adapter.DefaultMetrics("goquota_comprehensive")
	fmt.Println("   ✓ Structured logging (zerolog) configured")
	fmt.Println("   ✓ Prometheus metrics configured")

	// ============================================================
	// 2. Setup Storage (Redis + Fallback)
	// ============================================================
	fmt.Println("2. Setting up storage backends...")
	// Note: This assumes Redis is running (e.g., via docker-compose: docker-compose up -d redis)
	// Use REDIS_HOST environment variable for Docker networking, default to localhost for local dev
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisHost,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	defer redisClient.Close()

	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v\nMake sure Redis is running (e.g., via Docker: docker-compose up -d redis)", err)
	}
	fmt.Println("   ✓ Connected to Redis")

	// Create Redis storage
	primaryStorage, err := redisStorage.New(redisClient, redisStorage.Config{
		KeyPrefix:      "goquota:",
		EntitlementTTL: 24 * time.Hour,
		UsageTTL:       7 * 24 * time.Hour,
		MaxRetries:     3,
	})
	if err != nil {
		log.Fatalf("Failed to create Redis storage: %v", err)
	}
	fmt.Println("   ✓ Redis storage adapter created")

	// Create secondary storage (in-memory) for fallback
	secondaryStorage := memory.New()
	fmt.Println("   ✓ Secondary storage (in-memory) configured")

	// ============================================================
	// 3. Configure Quota Manager with All Features
	// ============================================================
	fmt.Println("3. Configuring quota manager...")
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				DailyQuotas: map[string]int{
					"api_calls": 50,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     20, // Allow burst up to 20 requests
					},
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5, 0.8, 0.9}, // Warn at 50%, 80%, 90%
				},
			},
			"pro": {
				Name: "pro",
				MonthlyQuotas: map[string]int{
					"api_calls": 10000,
				},
				DailyQuotas: map[string]int{
					"api_calls": 500,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "sliding_window",
						Rate:      100,
						Window:    time.Second,
					},
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5, 0.8, 0.9},
				},
			},
			"enterprise": {
				Name: "enterprise",
				MonthlyQuotas: map[string]int{
					"api_calls": 100000,
				},
				DailyQuotas: map[string]int{
					"api_calls": 5000,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "sliding_window",
						Rate:      500,
						Window:    time.Second,
					},
				},
				WarningThresholds: map[string][]float64{
					"api_calls": {0.5, 0.8, 0.9},
				},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:        true,
			EntitlementTTL: 5 * time.Minute,
			UsageTTL:       30 * time.Second,
		},
		FallbackConfig: &goquota.FallbackConfig{
			Enabled:                       true,
			FallbackToCache:               true,
			OptimisticAllowance:           true,
			OptimisticAllowancePercentage: 10.0, // Allow up to 10% optimistically
			SecondaryStorage:              secondaryStorage,
			MaxStaleness:                  5 * time.Minute,
		},
		WarningHandler: &warningHandler{},
		Metrics:        metrics,
		Logger:         logger,
	}

	manager, err := goquota.NewManager(primaryStorage, &config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	fmt.Println("   ✓ Quota manager created with all features enabled:")
	fmt.Println("     - Multiple tiers (free, pro, enterprise)")
	fmt.Println("     - Daily and monthly quotas")
	fmt.Println("     - Rate limiting (token bucket & sliding window)")
	fmt.Println("     - Caching enabled")
	fmt.Println("     - Fallback strategies enabled")
	fmt.Println("     - Warning thresholds configured")
	fmt.Println("     - Metrics and logging enabled")

	// ============================================================
	// 4. Programmatic Demonstrations
	// ============================================================
	fmt.Println("4. Running programmatic demonstrations...")

	// Setup test users
	user1 := "user1_free"
	user2 := "user2_pro"
	user3 := "user3_demo"

	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                user1,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}

	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                user2,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}

	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                user3,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}

	// Demo 1: Basic Quota Operations
	demoBasicQuotaOperations(ctx, manager, user1)

	// Demo 2: Rate Limiting
	demoRateLimiting(ctx, manager, user1, user2)

	// Demo 3: Idempotency Keys
	demoIdempotencyKeys(ctx, manager, user1)

	// Demo 4: Refunds
	demoRefunds(ctx, manager, user1)

	// Demo 5: Tier Changes & Proration
	demoTierChanges(ctx, manager, user3)

	// Demo 6: Soft Limits & Warnings
	demoSoftLimits(ctx, manager, user1)

	// Demo 7: Current Billing Cycle
	demoBillingCycle(ctx, manager, user1)

	// Demo 8: Fallback Strategies (documentation)
	demoFallbackStrategies()

	fmt.Println("5. Starting HTTP server with middleware...")

	// ============================================================
	// 5. HTTP Server with Middleware
	// ============================================================
	// Setup secondary storage with test users for fallback demo
	_ = secondaryStorage.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                user1,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Start Prometheus metrics server in background
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":9090", nil); err != nil {
			logger.Error("Metrics server failed", goquota.Field{Key: "error", Value: err})
		}
	}()

	// Create Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Mock authentication middleware
	r.Use(func(c *gin.Context) {
		userID := c.GetHeader("X-User-ID")
		if userID != "" {
			c.Set("UserID", userID)
		}
		c.Next()
	})

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	// Quota status endpoint (before middleware)
	r.GET("/api/quota", func(c *gin.Context) {
		userID := c.GetHeader("X-User-ID")
		if userID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing X-User-ID header"})
			return
		}

		// Get both daily and monthly usage
		dailyUsage, err := manager.GetQuota(c.Request.Context(), userID, "api_calls", goquota.PeriodTypeDaily)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		monthlyUsage, err := manager.GetQuota(c.Request.Context(), userID, "api_calls", goquota.PeriodTypeMonthly)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"daily": gin.H{
				"used":       dailyUsage.Used,
				"limit":      dailyUsage.Limit,
				"remaining":  dailyUsage.Limit - dailyUsage.Used,
				"percentage": float64(dailyUsage.Used) / float64(dailyUsage.Limit) * 100,
			},
			"monthly": gin.H{
				"used":       monthlyUsage.Used,
				"limit":      monthlyUsage.Limit,
				"remaining":  monthlyUsage.Limit - monthlyUsage.Used,
				"percentage": float64(monthlyUsage.Used) / float64(monthlyUsage.Limit) * 100,
			},
		})
	})

	// Protected API endpoints with middleware
	api := r.Group("/api")
	api.Use(ginMiddleware.Middleware(ginMiddleware.Config{
		Manager:     manager,
		GetUserID:   ginMiddleware.FromContext("UserID"),
		GetResource: ginMiddleware.FixedResource("api_calls"),
		GetAmount: ginMiddleware.DynamicCost(func(c *gin.Context) int {
			// Dynamic cost calculation: GET=1, POST=5, expensive endpoints=10
			if c.Request.URL.Path == "/api/expensive" {
				return 10
			}
			if c.Request.Method == "POST" {
				return 5
			}
			return 1
		}),
		PeriodType: goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(c *gin.Context, usage *goquota.Usage) {
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
		OnRateLimitExceeded: func(c *gin.Context, retryAfter time.Duration, info *goquota.RateLimitInfo) {
			c.Header("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":        "RATE_LIMIT_EXCEEDED",
					"message":     "Rate limit exceeded",
					"retry_after": retryAfter.Seconds(),
					"details": gin.H{
						"limit":     info.Limit,
						"remaining": info.Remaining,
						"reset_at":  info.ResetTime.Unix(),
					},
				},
			})
		},
	}))
	{
		api.GET("/data", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Data retrieved successfully",
				"cost":    1,
			})
		})

		api.POST("/write", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Write operation completed",
				"cost":    5,
			})
		})

		api.POST("/expensive", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Expensive operation completed",
				"cost":    10,
			})
		})
	}

	// Print server information
	fmt.Println("   ✓ HTTP server configured with:")
	fmt.Println("     - Health check endpoint: GET /health")
	fmt.Println("     - Quota status endpoint: GET /api/quota")
	fmt.Println("     - Protected endpoints: GET /api/data, POST /api/write, POST /api/expensive")
	fmt.Println("     - Dynamic cost calculation")
	fmt.Println("     - Custom error handlers")
	fmt.Println("     - Rate limit headers (X-RateLimit-*)")
	fmt.Println("\n=== Server Ready ===")
	fmt.Println("HTTP Server: http://localhost:8080")
	fmt.Println("Prometheus Metrics: http://localhost:9090/metrics")
	fmt.Println("\nNote: Redis should be running (e.g., via docker-compose: docker-compose up -d redis)")
	fmt.Println("\nTest commands:")
	fmt.Println("  curl -H \"X-User-ID: user1_free\" http://localhost:8080/api/data")
	fmt.Println("  curl -H \"X-User-ID: user1_free\" http://localhost:8080/api/quota")
	fmt.Println("  curl -X POST -H \"X-User-ID: user1_free\" http://localhost:8080/api/write")
	fmt.Println("\nPress Ctrl+C to stop.")

	// Start server
	if err := r.Run(":8080"); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}

// ============================================================
// Demo Functions
// ============================================================

func demoBasicQuotaOperations(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 1: Basic Quota Operations ---")

	// Get initial quota
	usage, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Initial monthly quota: %d/%d used\n", usage.Used, usage.Limit)

	// Consume monthly quota
	fmt.Println("Consuming 100 API calls (monthly)...")
	newUsed, err := manager.Consume(ctx, userID, "api_calls", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ Consumed. New total: %d/%d\n", newUsed, usage.Limit)
	}

	// Consume daily quota
	fmt.Println("Consuming 10 API calls (daily)...")
	dailyUsage, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeDaily)
	newUsedDaily, err := manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeDaily)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ Consumed. New daily total: %d/%d\n", newUsedDaily, dailyUsage.Limit)
	}

	// Get updated quota
	usage, _ = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Final monthly quota: %d/%d (%.1f%%)\n\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)
}

func demoRateLimiting(ctx context.Context, manager *goquota.Manager, freeUser, proUser string) {
	fmt.Println("--- Demo 2: Rate Limiting ---")

	// Test token bucket (free tier)
	fmt.Println("Free tier - Token Bucket (10 req/sec, burst 20):")
	fmt.Println("Making 25 rapid requests...")
	allowedCount := 0
	rateLimitedCount := 0
	for i := 0; i < 25; i++ {
		_, err := manager.Consume(ctx, freeUser, "api_calls", 1, goquota.PeriodTypeMonthly)
		if err != nil {
			var rateLimitErr *goquota.RateLimitExceededError
			if errors.As(err, &rateLimitErr) {
				rateLimitedCount++
			}
		} else {
			allowedCount++
		}
	}
	fmt.Printf("  Allowed: %d, Rate Limited: %d\n", allowedCount, rateLimitedCount)
	fmt.Printf("  Expected: ~20 allowed (burst capacity), ~5 rate limited\n")

	// Wait for token refill
	fmt.Println("Waiting 2 seconds for token refill...")
	time.Sleep(2 * time.Second)

	// Test sliding window (pro tier)
	fmt.Println("\nPro tier - Sliding Window (100 req/sec):")
	fmt.Println("Making 110 rapid requests...")
	allowedCount = 0
	rateLimitedCount = 0
	for i := 0; i < 110; i++ {
		_, err := manager.Consume(ctx, proUser, "api_calls", 1, goquota.PeriodTypeMonthly)
		if err != nil {
			var rateLimitErr *goquota.RateLimitExceededError
			if errors.As(err, &rateLimitErr) {
				rateLimitedCount++
			}
		} else {
			allowedCount++
		}
	}
	fmt.Printf("  Allowed: %d, Rate Limited: %d\n", allowedCount, rateLimitedCount)
	fmt.Printf("  Expected: ~100 allowed, ~10 rate limited\n\n")
}

func demoIdempotencyKeys(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 3: Idempotency Keys ---")

	idempotencyKey := "req_abc123_unique"

	// First request with idempotency key
	fmt.Printf("First request with idempotency key '%s'...\n", idempotencyKey)
	usage1, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	newUsed1, err := manager.Consume(
		ctx,
		userID,
		"api_calls",
		1,
		goquota.PeriodTypeMonthly,
		goquota.WithIdempotencyKey(idempotencyKey),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ Consumed. Usage: %d (was %d)\n", newUsed1, usage1.Used)
	}

	// Retry with same idempotency key (should not double-charge)
	fmt.Printf("\nRetry with same idempotency key '%s'...\n", idempotencyKey)
	newUsed2, err := manager.Consume(
		ctx,
		userID,
		"api_calls",
		1,
		goquota.PeriodTypeMonthly,
		goquota.WithIdempotencyKey(idempotencyKey),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ No double-charge! Usage: %d (same as before)\n", newUsed2)
		if newUsed1 == newUsed2 {
			fmt.Println("  ✓ Idempotency working correctly - same result returned")
		}
	}

	// New request with different key (should consume)
	fmt.Printf("\nNew request with different idempotency key 'req_xyz789'...\n")
	newUsed3, err := manager.Consume(
		ctx,
		userID,
		"api_calls",
		1,
		goquota.PeriodTypeMonthly,
		goquota.WithIdempotencyKey("req_xyz789"),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ Consumed. Usage: %d (increased from %d)\n", newUsed3, newUsed2)
	}
	fmt.Println()
}

func demoRefunds(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 4: Refunds ---")

	// Get initial usage
	usageBefore, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Usage before: %d/%d\n", usageBefore.Used, usageBefore.Limit)

	// Consume quota for an operation
	fmt.Println("Consuming 5 API calls for an operation...")
	_, err := manager.Consume(ctx, userID, "api_calls", 5, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	usageAfterConsume, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Usage after consume: %d/%d\n", usageAfterConsume.Used, usageAfterConsume.Limit)

	// Operation fails, refund the quota
	fmt.Println("\nOperation failed, refunding quota...")
	refundID := "refund_123"
	err = manager.Refund(ctx, &goquota.RefundRequest{
		UserID:         userID,
		Resource:       "api_calls",
		Amount:         5,
		PeriodType:     goquota.PeriodTypeMonthly,
		IdempotencyKey: refundID,
		Reason:         "Operation timeout",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("✓ Refund successful")
	}

	// Try to refund again with same key (should be idempotent)
	fmt.Println("Attempting duplicate refund with same idempotency key...")
	err = manager.Refund(ctx, &goquota.RefundRequest{
		UserID:         userID,
		Resource:       "api_calls",
		Amount:         5,
		PeriodType:     goquota.PeriodTypeMonthly,
		IdempotencyKey: refundID,
		Reason:         "Duplicate refund attempt",
	})
	if err != nil {
		fmt.Printf("  Error (expected for duplicate): %v\n", err)
	} else {
		fmt.Println("  ✓ Duplicate refund handled (idempotent)")
	}

	// Verify quota restored
	usageAfterRefund, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("\nUsage after refund: %d/%d\n", usageAfterRefund.Used, usageAfterRefund.Limit)
	if usageAfterRefund.Used == usageBefore.Used {
		fmt.Println("  ✓ Quota successfully restored to original value")
	}
	fmt.Println()
}

func demoTierChanges(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 5: Tier Changes & Proration ---")

	// Start with free tier
	fmt.Println("Starting with 'free' tier (1000 monthly quota)...")
	usage, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Initial usage: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)

	// Consume 50% of free tier quota
	fmt.Println("\nConsuming 500 API calls (50% of free tier)...")
	_, err := manager.Consume(ctx, userID, "api_calls", 500, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	usage, _ = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Usage after consumption: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)

	// Upgrade to pro tier mid-cycle
	fmt.Println("\nUpgrading to 'pro' tier mid-cycle (10000 monthly quota)...")
	err = manager.ApplyTierChange(ctx, userID, "free", "pro", "api_calls")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Get updated quota (should be prorated)
	usage, _ = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Usage after upgrade: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)
	fmt.Println("  ✓ Proration preserved: still at ~50% of new limit")
	fmt.Println()
}

func demoSoftLimits(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 6: Soft Limits & Warnings ---")
	fmt.Println("Warning thresholds configured: 50%, 80%, 90%")

	// Get current usage
	usage, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Current usage: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)

	// Calculate how much to consume to hit 80% threshold
	targetUsage := int(float64(usage.Limit) * 0.8)
	toConsume := targetUsage - usage.Used
	if toConsume > 0 {
		fmt.Printf("\nConsuming %d API calls to reach 80%% threshold...\n", toConsume)
		_, err := manager.Consume(ctx, userID, "api_calls", toConsume, goquota.PeriodTypeMonthly)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println("  ✓ Warning callback should have been triggered at 80%")
		}
	} else {
		fmt.Println("  Usage already exceeds 80% threshold")
	}

	usage, _ = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("Final usage: %d/%d (%.1f%%)\n\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)
}

func demoBillingCycle(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 7: Anniversary-Based Billing Cycle ---")

	period, err := manager.GetCurrentCycle(ctx, userID)
	if err != nil {
		fmt.Printf("Error: %v\n\n", err)
		return
	}

	fmt.Printf("Subscription start: %s\n", period.Start.Format("2006-01-02 15:04:05"))
	fmt.Printf("Current cycle start: %s\n", period.Start.Format("2006-01-02 15:04:05"))
	fmt.Printf("Current cycle end:   %s\n", period.End.Format("2006-01-02 15:04:05"))
	fmt.Printf("Days remaining: %.0f\n", time.Until(period.End).Hours()/24)
	fmt.Println("  ✓ Cycles follow subscription anniversary, not calendar months")
	fmt.Println()
}

func demoFallbackStrategies() {
	fmt.Println("--- Demo 8: Fallback Strategies ---")
	fmt.Println("Fallback strategies are configured and active:")
	fmt.Println("  1. Cache Fallback: Enabled (5 min max staleness)")
	fmt.Println("     - Uses cached data when primary storage fails")
	fmt.Println("  2. Optimistic Allowance: Enabled (10% of quota)")
	fmt.Println("     - Allows consumption up to 10% optimistically")
	fmt.Println("     - Useful during storage outages")
	fmt.Println("  3. Secondary Storage: Configured (in-memory)")
	fmt.Println("     - Falls back to secondary storage if available")
	fmt.Println()
	fmt.Println("When primary storage (Redis) is unavailable:")
	fmt.Println("  - System tries cache first (if data is fresh)")
	fmt.Println("  - Falls back to secondary storage")
	fmt.Println("  - Allows optimistic consumption as last resort")
	fmt.Println("  - All fallback usage is tracked for reconciliation")
	fmt.Println()
}

// Warning handler implementation
type warningHandler struct{}

func (h *warningHandler) OnWarning(ctx context.Context, usage *goquota.Usage, threshold float64) {
	pct := float64(usage.Used) / float64(usage.Limit) * 100
	fmt.Printf("  ⚠️  WARNING: User %s reached %.0f%% threshold (%.1f%% used, %d/%d)\n",
		usage.UserID, threshold*100, pct, usage.Used, usage.Limit)
	// In production, you might:
	// - Send email/SMS alerts
	// - Log to monitoring system
	// - Trigger webhooks
}
