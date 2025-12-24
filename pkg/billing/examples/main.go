package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/billing/revenuecat"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// 1. Create goquota manager
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "explorer",
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: "explorer",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
			"fluent": {
				Name: "fluent",
				MonthlyQuotas: map[string]int{
					"api_calls": 10000,
				},
			},
		},
	}
	manager, err := goquota.NewManager(storage, config)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create RevenueCat billing provider
	provider, err := revenuecat.NewProvider(billing.Config{
		Manager: manager,
		TierMapping: map[string]string{
			"scholar_monthly": "scholar",
			"scholar_annual":  "scholar",
			"fluent_monthly":  "fluent",
			"fluent_annual":   "fluent",
			"*":               "explorer", // Default tier for unknown entitlements
		},
		WebhookSecret: os.Getenv("REVENUECAT_WEBHOOK_SECRET"),
		APIKey:        os.Getenv("REVENUECAT_SECRET_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	// 3. Register webhook endpoint
	// RevenueCat will send webhook events to this endpoint
	http.Handle("/webhooks/revenuecat", provider.WebhookHandler())

	// 4. Register restore purchases endpoint
	// Users can call this to sync their subscription status
	http.HandleFunc("/restore-purchases", func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "user_id required", http.StatusBadRequest)
			return
		}

		tier, err := provider.SyncUser(r.Context(), userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"user_id": userID,
			"tier":    tier,
		})
	})

	// 5. Example: Check quota (using goquota manager)
	http.HandleFunc("/api/check-quota", func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "user_id required", http.StatusBadRequest)
			return
		}

		usage, err := manager.GetQuota(r.Context(), userID, "api_calls", goquota.PeriodTypeMonthly)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user_id":   userID,
			"tier":      usage.Tier,
			"used":      usage.Used,
			"limit":     usage.Limit,
			"remaining": usage.Limit - usage.Used,
		})
	})

	// 6. Start server
	log.Println("Server starting on :8080")
	log.Println("Webhook endpoint: http://localhost:8080/webhooks/revenuecat")
	log.Println("Restore purchases: http://localhost:8080/restore-purchases?user_id=USER_ID")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
