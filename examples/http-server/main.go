// Package main demonstrates HTTP middleware usage with goquota
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	httpMiddleware "github.com/mihaimyh/goquota/middleware/http"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// Create in-memory storage for demo
	storage := memory.New()

	// Configure quota tiers
	config := goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"api_calls": 10, // 10 calls per day
				},
			},
			"pro": {
				Name: "pro",
				DailyQuotas: map[string]int{
					"api_calls": 1000, // 1000 calls per day
				},
			},
		},
	}

	// Create quota manager
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Set up some test users
	ctx := context.Background()
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user2",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Create HTTP server with quota middleware
	mux := http.NewServeMux()

	// Protected endpoint with quota enforcement
	protectedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		fmt.Fprintf(w, "Hello, %s! Your request was processed.\n", userID)
	})

	// Apply quota middleware
	quotaMiddleware := httpMiddleware.Middleware(&httpMiddleware.Config{
		Manager:     manager,
		GetUserID:   httpMiddleware.FromHeader("X-User-ID"),
		GetResource: httpMiddleware.FixedResource("api_calls"),
		GetAmount:   httpMiddleware.FixedAmount(1),
		PeriodType:  goquota.PeriodTypeDaily,
		OnQuotaExceeded: func(w http.ResponseWriter, r *http.Request, usage *goquota.Usage) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error": "quota_exceeded", "used": %d, "limit": %d, "tier": "%s"}`,
				usage.Used, usage.Limit, usage.Tier)
		},
	})

	mux.Handle("/api/protected", quotaMiddleware(protectedHandler))

	// Health check endpoint (no quota)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "OK")
	})

	// Quota status endpoint
	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			http.Error(w, "Missing X-User-ID header", http.StatusBadRequest)
			return
		}

		usage, err := manager.GetQuota(r.Context(), userID, "api_calls", goquota.PeriodTypeDaily)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"used": %d, "limit": %d, "tier": "%s", "remaining": %d}`,
			usage.Used, usage.Limit, usage.Tier, usage.Limit-usage.Used)
	})

	// Start server
	addr := ":8080"
	fmt.Printf("Server starting on %s\n", addr)
	fmt.Println("\nTry these commands:")
	fmt.Println("  curl -H \"X-User-ID: user1\" http://localhost:8080/api/protected")
	fmt.Println("  curl -H \"X-User-ID: user1\" http://localhost:8080/api/quota")
	fmt.Println("  curl -H \"X-User-ID: user2\" http://localhost:8080/api/protected")
	fmt.Println()

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
