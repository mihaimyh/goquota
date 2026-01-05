package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	httpMiddleware "github.com/mihaimyh/goquota/middleware/http"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// Create storage
	storage := memory.New()

	// Configure tiers with rate limits
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				// Rate limits: 10 requests per second with burst of 20
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     20,
					},
				},
			},
			"pro": {
				Name: "pro",
				MonthlyQuotas: map[string]int{
					"api_calls": 10000,
				},
				// Rate limits: 100 requests per second with burst of 200
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "sliding_window",
						Rate:      100,
						Window:    time.Second,
						Burst:     0, // Not used for sliding window
					},
				},
			},
		},
	}

	// Create manager
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		log.Fatal(err)
	}

	// Set up user entitlements
	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user2",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Demonstrate rate limiting
	fmt.Println("=== Rate Limiting Demo ===")

	// Test 1: Token bucket with burst
	fmt.Println("Test 1: Token Bucket (Free tier - 10 req/sec, burst 20)")
	fmt.Println("Making 25 rapid requests...")
	allowedCount := 0
	rateLimitedCount := 0

	for range 25 {
		_, err := manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
		if err != nil {
			var rateLimitErr *goquota.RateLimitExceededError
			if errors.As(err, &rateLimitErr) {
				rateLimitedCount++
			} else {
				log.Printf("Unexpected error: %v", err)
			}
		} else {
			allowedCount++
		}
	}

	fmt.Printf("Allowed: %d, Rate Limited: %d\n", allowedCount, rateLimitedCount)
	fmt.Printf("Expected: ~20 allowed (burst capacity), ~5 rate limited\n\n")

	// Wait for refill
	fmt.Println("Waiting 2 seconds for token refill...")
	time.Sleep(2 * time.Second)

	// Test 2: Sliding window
	fmt.Println("Test 2: Sliding Window (Pro tier - 100 req/sec)")
	fmt.Println("Making 110 rapid requests...")
	allowedCount = 0
	rateLimitedCount = 0

	for i := 0; i < 110; i++ {
		_, err := manager.Consume(ctx, "user2", "api_calls", 1, goquota.PeriodTypeMonthly)
		if err != nil {
			var rateLimitErr *goquota.RateLimitExceededError
			if errors.As(err, &rateLimitErr) {
				rateLimitedCount++
			} else {
				log.Printf("Unexpected error: %v", err)
			}
		} else {
			allowedCount++
		}
	}

	fmt.Printf("Allowed: %d, Rate Limited: %d\n", allowedCount, rateLimitedCount)
	fmt.Printf("Expected: ~100 allowed, ~10 rate limited\n\n")

	// Test 3: HTTP Middleware
	fmt.Println("Test 3: HTTP Middleware Integration")
	fmt.Println("Starting HTTP server on :8080...")
	fmt.Println("Try: curl -H 'X-User-ID: user1' http://localhost:8080/api")

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Create middleware config
	middlewareConfig := &httpMiddleware.Config{
		Manager: manager,
		GetUserID: func(r *http.Request) string {
			return r.Header.Get("X-User-ID")
		},
		GetResource: httpMiddleware.FixedResource("api_calls"),
		GetAmount:   httpMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeMonthly,
		OnQuotaExceeded: func(w http.ResponseWriter, r *http.Request, usage *goquota.Usage) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprintf(w, "Quota exceeded: %d/%d used", usage.Used, usage.Limit)
		},
	}

	handler := httpMiddleware.Middleware(middlewareConfig)(mux)

	server := &http.Server{
		Addr:    ":8080",
		Handler: handler,
	}

	fmt.Println("\nServer running. Press Ctrl+C to stop.")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
