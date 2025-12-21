// Package main demonstrates goquota integration with Chi router
package main

import (
"context"
"fmt"
"log"
"net/http"
"time"

"github.com/go-chi/chi/v5"
"github.com/go-chi/chi/v5/middleware"

"github.com/mihaimyh/goquota/pkg/goquota"
httpMiddleware "github.com/mihaimyh/goquota/middleware/http"
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

manager, err := goquota.NewManager(storage, config)
if err != nil {
log.Fatalf("Failed to create manager: %v", err)
}

// Set up test users
ctx := context.Background()
_ = manager.SetEntitlement(ctx, &goquota.Entitlement{
UserID:                "user1",
Tier:                  "free",
SubscriptionStartDate: time.Now().UTC(),
UpdatedAt:             time.Now().UTC(),
})

// Create Chi router
r := chi.NewRouter()

// Chi middleware
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)

// Create quota middleware
quotaMiddleware := httpMiddleware.Middleware(httpMiddleware.Config{
Manager:     manager,
GetUserID:   httpMiddleware.FromHeader("X-User-ID"),
GetResource: httpMiddleware.FixedResource("api_calls"),
GetAmount:   httpMiddleware.FixedAmount(1),
PeriodType:  goquota.PeriodTypeDaily,
})

// Public routes
r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
fmt.Fprintln(w, "OK")
})

r.Get("/quota", func(w http.ResponseWriter, r *http.Request) {
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
fmt.Fprintf(w, `{"used": %d, "limit": %d, "remaining": %d}`,
usage.Used, usage.Limit, usage.Limit-usage.Used)
})

// Protected routes with quota
r.Route("/api", func(r chi.Router) {
r.Use(func(next http.Handler) http.Handler {
return quotaMiddleware(next)
})

r.Get("/data", func(w http.ResponseWriter, r *http.Request) {
fmt.Fprintln(w, "Data retrieved successfully")
})

r.Post("/process", func(w http.ResponseWriter, r *http.Request) {
fmt.Fprintln(w, "Processing complete")
})
})

// Start server
addr := ":8080"
fmt.Printf("Chi router server starting on %s\n", addr)
fmt.Println("Try: curl -H \"X-User-ID: user1\" http://localhost:8080/api/data")

if err := http.ListenAndServe(addr, r); err != nil {
log.Fatalf("Server failed: %v", err)
}
}