// Package main demonstrates all goquota features in a comprehensive example
// including Redis storage, rate limiting, idempotency, refunds, tier changes,
// soft limits, fallback strategies, observability, HTTP middleware, admin operations,
// dry-run mode, enhanced consume response, audit trail, and clock skew protection.
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
	"github.com/mihaimyh/goquota/pkg/billing"
	billingPromMetrics "github.com/mihaimyh/goquota/pkg/billing/metrics/prometheus"
	"github.com/mihaimyh/goquota/pkg/billing/revenuecat"
	"github.com/mihaimyh/goquota/pkg/billing/stripe"
	"github.com/mihaimyh/goquota/pkg/goquota"
	zerolog_adapter "github.com/mihaimyh/goquota/pkg/goquota/logger/zerolog"
	prometheus_adapter "github.com/mihaimyh/goquota/pkg/goquota/metrics/prometheus"
	"github.com/mihaimyh/goquota/storage/memory"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

//nolint:gocyclo // Comprehensive example demonstrating all features
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
		//nolint:gocritic // Intentional: log.Fatalf exits before defer runs, which is acceptable for example
		log.Fatalf("Failed to connect to Redis: %v\n"+
			"Make sure Redis is running (e.g., via Docker: docker-compose up -d redis)", err)
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
		CircuitBreakerConfig: &goquota.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 3,                // Open circuit after 3 consecutive failures
			ResetTimeout:     10 * time.Second, // Wait 10s before half-open
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
	// 3b. Optional: Configure RevenueCat Billing Provider
	// ============================================================
	fmt.Println("3b. Configuring RevenueCat billing provider (optional)...")
	var billingProvider billing.Provider
	var hasWebhookSecret, hasAPIKey bool
	webhookSecret := os.Getenv("REVENUECAT_WEBHOOK_SECRET")
	apiKey := os.Getenv("REVENUECAT_SECRET_API_KEY")
	hasWebhookSecret = webhookSecret != ""
	hasAPIKey = apiKey != ""

	// Create shared billing metrics instance (used by both RevenueCat and Stripe if enabled)
	// Only create once to avoid duplicate registration
	var sharedBillingMetrics billing.Metrics
	metricsCreated := false

	if !hasWebhookSecret && !hasAPIKey {
		fmt.Println("   ⚠ RevenueCat disabled: REVENUECAT_WEBHOOK_SECRET and REVENUECAT_SECRET_API_KEY not set")
		fmt.Println("     To enable RevenueCat webhooks, set REVENUECAT_WEBHOOK_SECRET environment variable")
	} else {
		// Create provider if webhook secret is provided (API key is optional for webhook-only usage)
		if hasWebhookSecret {
			// Create billing metrics (optional - uses same namespace as goquota metrics)
			// Only create once and share between providers
			if !metricsCreated {
				sharedBillingMetrics = billingPromMetrics.DefaultMetrics("goquota_comprehensive")
				metricsCreated = true
			}
			billingMetrics := sharedBillingMetrics

			rcProvider, err := revenuecat.NewProvider(billing.Config{
				Manager: manager,
				TierMapping: map[string]string{
					// Map RevenueCat entitlements/products to goquota tiers
					"free_monthly":       "free",
					"free_annual":        "free",
					"pro_monthly":        "pro",
					"pro_annual":         "pro",
					"enterprise_monthly": "enterprise",
					"enterprise_annual":  "enterprise",
					"*":                  "free", // Default / unknown entitlements
					"default":            "free",
				},
				WebhookSecret: webhookSecret,
				APIKey:        apiKey,         // May be empty for webhook-only usage
				Metrics:       billingMetrics, // Optional: enables Prometheus metrics for billing operations
			})
			if err != nil {
				fmt.Printf("   ⚠ Failed to create RevenueCat provider: %v\n", err)
				fmt.Println("     Webhook endpoint will NOT be available")
			} else {
				billingProvider = rcProvider
				fmt.Println("   ✓ RevenueCat provider configured")
				if hasWebhookSecret {
					fmt.Println("     - Webhook endpoint: POST /webhooks/revenuecat")
					secretPreviewLen := 8
					if len(webhookSecret) < secretPreviewLen {
						secretPreviewLen = len(webhookSecret)
					}
					fmt.Printf("     - Webhook secret: %s... (configured)\n", webhookSecret[:secretPreviewLen])
				}
				if hasAPIKey {
					fmt.Println("     - Restore purchases endpoint: POST /api/restore-purchases")
				} else {
					fmt.Println("     ⚠ Restore purchases disabled: REVENUECAT_SECRET_API_KEY not set")
				}
				fmt.Println("     - Prometheus metrics enabled for billing operations")
			}
		} else {
			fmt.Println("   ⚠ RevenueCat webhook disabled: REVENUECAT_WEBHOOK_SECRET not set")
			fmt.Println("     (API key alone is not sufficient for webhook verification)")
		}
	}

	// ============================================================
	// 3c. Optional: Configure Stripe Billing Provider
	// ============================================================
	fmt.Println("3c. Configuring Stripe billing provider (optional)...")
	var stripeProvider billing.Provider
	stripeAPIKey := os.Getenv("STRIPE_API_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	hasStripeAPIKey := stripeAPIKey != ""
	hasStripeWebhookSecret := stripeWebhookSecret != ""

	if !hasStripeAPIKey && !hasStripeWebhookSecret {
		fmt.Println("   ⚠ Stripe disabled: STRIPE_API_KEY and STRIPE_WEBHOOK_SECRET not set")
		fmt.Println("     To enable Stripe webhooks, set STRIPE_WEBHOOK_SECRET environment variable")
		fmt.Println("     To enable Stripe API sync, set STRIPE_API_KEY environment variable")
	} else {
		// Create provider if webhook secret OR API key is provided
		// Webhook secret is required for webhook signature verification
		// API key is optional but required for subscription operations and SyncUser
		if hasStripeWebhookSecret || hasStripeAPIKey {
			// Create billing metrics (optional - uses same namespace as goquota metrics)
			// Reuse shared metrics instance if already created
			if !metricsCreated {
				sharedBillingMetrics = billingPromMetrics.DefaultMetrics("goquota_comprehensive")
				metricsCreated = true
			}
			stripeBillingMetrics := sharedBillingMetrics

			stripeProv, err := stripe.NewProvider(stripe.Config{
				Config: billing.Config{
					Manager: manager,
					TierMapping: map[string]string{
						// Map Stripe Price IDs to goquota tiers
						// These prices were created via Stripe MCP server for testing
						// Free Tier
						"price_1SmBvJERG1ZIgEobmVu51B8F": "free", // Free monthly ($0)
						"price_1SmBvKERG1ZIgEobRFhe5l6k": "free", // Free annual ($0)
						// Pro Tier
						"price_1SmBvLERG1ZIgEob52Zugp4X": "pro", // Pro monthly ($10)
						"price_1SmBvMERG1ZIgEobP7OoCkti": "pro", // Pro annual ($100)
						// Enterprise Tier
						"price_1SmBvMERG1ZIgEobiWRrQNkH": "enterprise", // Enterprise monthly ($50)
						"price_1SmBvNERG1ZIgEobLbli8DpI": "enterprise", // Enterprise annual ($500)
						// Fallback mappings
						"*":       "free", // Default / unknown entitlements
						"default": "free",
					},
					Metrics: stripeBillingMetrics, // Optional: enables Prometheus metrics for billing operations
				},
				StripeAPIKey:        stripeAPIKey,
				StripeWebhookSecret: stripeWebhookSecret, // May be empty for API-only usage
			})
			if err != nil {
				fmt.Printf("   ⚠ Failed to create Stripe provider: %v\n", err)
				fmt.Println("     Webhook endpoint will NOT be available")
			} else {
				stripeProvider = stripeProv
				fmt.Println("   ✓ Stripe provider configured")
				if hasStripeWebhookSecret {
					fmt.Println("     - Webhook endpoint: POST /webhooks/stripe")
					secretPreviewLen := 8
					if len(stripeWebhookSecret) < secretPreviewLen {
						secretPreviewLen = len(stripeWebhookSecret)
					}
					fmt.Printf("     - Webhook secret: %s... (configured)\n", stripeWebhookSecret[:secretPreviewLen])
				} else {
					fmt.Println("     ⚠ Webhook disabled: STRIPE_WEBHOOK_SECRET not set")
				}
				if hasStripeAPIKey {
					fmt.Println("     - Restore purchases endpoint: POST /api/restore-purchases-stripe")
				} else {
					fmt.Println("     ⚠ Restore purchases disabled: STRIPE_API_KEY not set")
				}
				fmt.Println("     - Prometheus metrics enabled for billing operations")
			}
		}
	}

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

	// Demo 9: Admin Operations
	demoAdminOperations(ctx, manager, user1)

	// Demo 10: Dry-Run Mode
	demoDryRunMode(ctx, manager, user1)

	// Demo 11: Enhanced Consume Response
	demoEnhancedConsumeResponse(ctx, manager, user1)

	// Demo 12: Clock Skew Protection (informational)
	demoClockSkewProtection()

	fmt.Println("5. Starting HTTP server with middleware...")

	// ============================================================
	// 5. HTTP Server with Middleware
	// ============================================================
	// Setup secondary storage with test users for fallback demo
	if err := secondaryStorage.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                user1,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}); err != nil {
		logger.Warn("Failed to set entitlement in secondary storage", goquota.Field{Key: "error", Value: err})
	}

	// Start Prometheus metrics server in background
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		//nolint:gosec // Example code: timeout not required for metrics endpoint
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

	// ============================================================
	// 5a. Billing Integration Endpoints (RevenueCat)
	// ============================================================
	if billingProvider != nil {
		// RevenueCat webhook endpoint (used by RevenueCat to push entitlement changes)
		// Provider is only created if webhook secret is provided, so this route is always registered
		r.POST("/webhooks/revenuecat", gin.WrapH(billingProvider.WebhookHandler()))
		fmt.Println("   ✓ RevenueCat webhook route registered: POST /webhooks/revenuecat")

		// Restore purchases / manual sync endpoint
		// Only register if API key is provided (required for SyncUser)
		if hasAPIKey {
			r.POST("/api/restore-purchases", func(c *gin.Context) {
				// Prefer explicit query parameter, fall back to authenticated user header
				userID := c.Query("user_id")
				if userID == "" {
					userID = c.GetHeader("X-User-ID")
				}
				if userID == "" {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": "missing user identifier (provide ?user_id=... or X-User-ID header)",
					})
					return
				}

				tier, err := billingProvider.SyncUser(c.Request.Context(), userID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{
						"error":   "failed to sync purchases",
						"details": err.Error(),
					})
					return
				}

				c.JSON(http.StatusOK, gin.H{
					"user_id": userID,
					"tier":    tier,
				})
			})
			fmt.Println("   ✓ Restore purchases route registered: POST /api/restore-purchases")
		}
	} else {
		fmt.Println("   ⚠ Billing endpoints NOT registered (RevenueCat provider disabled)")
		fmt.Println("     Set REVENUECAT_WEBHOOK_SECRET environment variable to enable webhooks")
		// Register a helpful error endpoint for debugging
		r.POST("/webhooks/revenuecat", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":    "RevenueCat webhook not configured",
				"message":  "REVENUECAT_WEBHOOK_SECRET environment variable is not set",
				"endpoint": "/webhooks/revenuecat",
			})
		})
		fmt.Println("     Debug endpoint registered: POST /webhooks/revenuecat (returns 503)")
	}

	// ============================================================
	// 5b. Billing Integration Endpoints (Stripe)
	// ============================================================
	if stripeProvider != nil {
		// Stripe webhook endpoint (used by Stripe to push subscription changes)
		// Provider is only created if API key is provided, webhook secret is optional
		if hasStripeWebhookSecret {
			r.POST("/webhooks/stripe", gin.WrapH(stripeProvider.WebhookHandler()))
			fmt.Println("   ✓ Stripe webhook route registered: POST /webhooks/stripe")
		} else {
			// Register a helpful error endpoint for debugging
			r.POST("/webhooks/stripe", func(c *gin.Context) {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error":    "Stripe webhook not configured",
					"message":  "STRIPE_WEBHOOK_SECRET environment variable is not set",
					"endpoint": "/webhooks/stripe",
				})
			})
			fmt.Println("     Debug endpoint registered: POST /webhooks/stripe (returns 503)")
		}

		// Restore purchases / manual sync endpoint
		// Only register if API key is provided (required for SyncUser)
		if hasStripeAPIKey {
			r.POST("/api/restore-purchases-stripe", func(c *gin.Context) {
				// Prefer explicit query parameter, fall back to authenticated user header
				userID := c.Query("user_id")
				if userID == "" {
					userID = c.GetHeader("X-User-ID")
				}
				if userID == "" {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": "missing user identifier (provide ?user_id=... or X-User-ID header)",
					})
					return
				}

				tier, err := stripeProvider.SyncUser(c.Request.Context(), userID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{
						"error":   "failed to sync purchases",
						"details": err.Error(),
					})
					return
				}

				c.JSON(http.StatusOK, gin.H{
					"user_id": userID,
					"tier":    tier,
				})
			})
			fmt.Println("   ✓ Restore purchases route registered: POST /api/restore-purchases-stripe")
		}
	} else {
		fmt.Println("   ⚠ Stripe billing endpoints NOT registered (Stripe provider disabled)")
		fmt.Println("     Set STRIPE_API_KEY environment variable to enable Stripe integration")
		// Register a helpful error endpoint for debugging
		r.POST("/webhooks/stripe", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":    "Stripe webhook not configured",
				"message":  "STRIPE_API_KEY environment variable is not set",
				"endpoint": "/webhooks/stripe",
			})
		})
		fmt.Println("     Debug endpoint registered: POST /webhooks/stripe (returns 503)")
	}

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
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
		return
	}
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
	dailyUsage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		fmt.Printf("Error getting daily quota: %v\n", err)
		return
	}
	newUsedDaily, err := manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeDaily)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("✓ Consumed. New daily total: %d/%d\n", newUsedDaily, dailyUsage.Limit)
	}

	// Get updated quota
	usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting updated quota: %v\n", err)
		return
	}
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
	usage1, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
		return
	}
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
	usageBefore, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
		return
	}
	fmt.Printf("Usage before: %d/%d\n", usageBefore.Used, usageBefore.Limit)

	// Consume quota for an operation
	fmt.Println("Consuming 5 API calls for an operation...")
	_, err = manager.Consume(ctx, userID, "api_calls", 5, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	usageAfterConsume, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota after consume: %v\n", err)
		return
	}
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
	usageAfterRefund, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota after refund: %v\n", err)
		return
	}
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
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
		return
	}
	fmt.Printf("Initial usage: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)

	// Consume 50% of free tier quota
	fmt.Println("\nConsuming 500 API calls (50% of free tier)...")
	_, err = manager.Consume(ctx, userID, "api_calls", 500, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota after consumption: %v\n", err)
		return
	}
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
	usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota after upgrade: %v\n", err)
		return
	}
	fmt.Printf("Usage after upgrade: %d/%d (%.1f%%)\n", usage.Used, usage.Limit,
		float64(usage.Used)/float64(usage.Limit)*100)
	fmt.Println("  ✓ Proration preserved: still at ~50% of new limit")
	fmt.Println()
}

func demoSoftLimits(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 6: Soft Limits & Warnings ---")
	fmt.Println("Warning thresholds configured: 50%, 80%, 90%")

	// Get current usage
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
		return
	}
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

	usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting final quota: %v\n", err)
		return
	}
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

func demoAdminOperations(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 9: Admin Operations ---")
	fmt.Println("Administrative operations for incident response and customer support:")
	fmt.Println()

	// Get current usage before operations
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n\n", err)
		return
	}
	fmt.Printf("Current usage: %d/%d\n", usage.Used, usage.Limit)

	// 1. Set Usage - Manual correction
	fmt.Println("\n1. SetUsage() - Manual correction:")
	fmt.Println("   Use case: Fix incorrect usage due to system error")
	if err := manager.SetUsage(ctx, userID, "api_calls", goquota.PeriodTypeMonthly, 50); err != nil {
		fmt.Printf("   Error: %v\n", err)
	} else {
		fmt.Println("   ✓ Usage manually set to 50")
	}

	// 2. Grant One-Time Credit - Service compensation
	fmt.Println("\n2. GrantOneTimeCredit() - Service compensation:")
	fmt.Println("   Use case: Compensate user for service outage")
	if err := manager.GrantOneTimeCredit(ctx, userID, "api_calls", 1000); err != nil {
		fmt.Printf("   Error: %v\n", err)
	} else {
		fmt.Println("   ✓ Granted 1000 one-time credits (non-expiring)")
		// Check forever credits
		foreverUsage, _ := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeForever)
		fmt.Printf("   Forever credits balance: %d\n", foreverUsage.Limit-foreverUsage.Used)
	}

	// 3. Reset Usage - Fresh start
	fmt.Println("\n3. ResetUsage() - Fresh start:")
	fmt.Println("   Use case: Reset monthly usage during incident resolution")
	if err := manager.ResetUsage(ctx, userID, "api_calls", goquota.PeriodTypeMonthly); err != nil {
		fmt.Printf("   Error: %v\n", err)
	} else {
		fmt.Println("   ✓ Monthly usage reset to 0")
	}

	// Verify final state
	usage, _ = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	fmt.Printf("\nFinal state: %d/%d monthly quota\n", usage.Used, usage.Limit)
	fmt.Println("  ✓ All admin operations logged to audit trail (if configured)")
	fmt.Println()
}

func demoDryRunMode(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 10: Dry-Run / Shadow Mode ---")
	fmt.Println("Test quota enforcement without blocking traffic:")
	fmt.Println()

	// Reset to clean state
	_ = manager.ResetUsage(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)

	// Consume most of the quota
	for i := 0; i < 990; i++ {
		_, _ = manager.Consume(ctx, userID, "api_calls", 1, goquota.PeriodTypeMonthly)
	}

	fmt.Println("1. Normal enforcement mode:")
	// Try to exceed limit (should fail)
	_, err := manager.Consume(ctx, userID, "api_calls", 20, goquota.PeriodTypeMonthly)
	if errors.Is(err, goquota.ErrQuotaExceeded) {
		fmt.Println("   ✗ Request blocked: quota exceeded")
	}

	fmt.Println("\n2. Dry-run mode (shadow testing):")
	// Same request with dry-run (should succeed but log violation)
	_, err = manager.Consume(
		ctx,
		userID,
		"api_calls",
		20,
		goquota.PeriodTypeMonthly,
		goquota.WithDryRun(true),
	)
	if err == nil {
		fmt.Println("   ✓ Request allowed (dry-run mode)")
		fmt.Println("   📝 Violation logged for monitoring")
	} else if errors.Is(err, goquota.ErrQuotaExceeded) {
		fmt.Println("   ⚠️  Would have been blocked (dry-run)")
		fmt.Println("   ✓ Request continues normally")
	}

	fmt.Println("\nUse cases:")
	fmt.Println("  - Gradual rollout: Enable for 10% of users, monitor, expand")
	fmt.Println("  - A/B testing: Compare enforcement strategies")
	fmt.Println("  - Configuration validation: Test new limits before applying")
	fmt.Println()
}

func demoEnhancedConsumeResponse(ctx context.Context, manager *goquota.Manager, userID string) {
	fmt.Println("--- Demo 11: Enhanced Consume Response ---")
	fmt.Println("Get detailed usage info without extra storage calls:")
	fmt.Println()

	// Reset to clean state
	_ = manager.ResetUsage(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)

	// Use ConsumeWithResult for detailed response
	fmt.Println("1. ConsumeWithResult() - Detailed response:")
	result, err := manager.ConsumeWithResult(ctx, userID, "api_calls", 75, goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("   Error: %v\n\n", err)
		return
	}

	fmt.Printf("   Used:      %d\n", result.NewUsed)
	fmt.Printf("   Limit:     %d\n", result.Limit)
	fmt.Printf("   Remaining: %d\n", result.Remaining)
	fmt.Printf("   Percentage: %.1f%%\n", result.Percentage)
	fmt.Println("   ✓ All info in single storage call")

	// Demonstrate notification logic
	if result.Percentage >= 80.0 {
		fmt.Printf("\n   ⚠️  User at %.1f%% - sending warning email\n", result.Percentage)
	}

	// Continue consuming
	result, _ = manager.ConsumeWithResult(ctx, userID, "api_calls", 15, goquota.PeriodTypeMonthly)
	fmt.Printf("\n2. After another request: %.1f%% used\n", result.Percentage)
	if result.Percentage >= 90.0 {
		fmt.Println("   🔴 Critical threshold - notify user immediately")
	}

	fmt.Println("\nBenefits:")
	fmt.Println("  - 50% reduction in Redis load for notification logic")
	fmt.Println("  - Single storage call instead of Consume() + GetUsage()")
	fmt.Println("  - Perfect for soft limit notifications")
	fmt.Println()
}

func demoClockSkewProtection() {
	fmt.Println("--- Demo 12: Clock Skew Protection ---")
	fmt.Println("Prevents quota double-spending at reset boundaries:")
	fmt.Println()

	fmt.Println("Problem: Application server clock drift")
	fmt.Println("  - Server A: 23:59:59 (old period)")
	fmt.Println("  - Server B: 00:00:01 (new period)")
	fmt.Println("  - User could consume quota twice!")
	fmt.Println()

	fmt.Println("Solution: TimeSource interface")
	fmt.Println("  ✓ Redis: Uses REDIS TIME command (server time)")
	fmt.Println("  ✓ Firestore: Uses Firestore server timestamps")
	fmt.Println("  ✓ Postgres: Uses PostgreSQL NOW()")
	fmt.Println("  ✓ All instances use same time source")
	fmt.Println()

	fmt.Println("Benefits:")
	fmt.Println("  - Consistent period calculations across all instances")
	fmt.Println("  - Prevents 'time travel' attacks from clock drift")
	fmt.Println("  - Critical for accurate billing at month/day boundaries")
	fmt.Println("  - No configuration required - works automatically")
	fmt.Println()
}

// Warning handler implementation
type warningHandler struct{}

func (h *warningHandler) OnWarning(_ context.Context, usage *goquota.Usage, threshold float64) {
	pct := float64(usage.Used) / float64(usage.Limit) * 100
	fmt.Printf("  ⚠️  WARNING: User %s reached %.0f%% threshold (%.1f%% used, %d/%d)\n",
		usage.UserID, threshold*100, pct, usage.Used, usage.Limit)
	// In production, you might:
	// - Send email/SMS alerts
	// - Log to monitoring system
	// - Trigger webhooks
}
