package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	zerolog_adapter "github.com/mihaimyh/goquota/pkg/goquota/logger/zerolog"
	prometheus_adapter "github.com/mihaimyh/goquota/pkg/goquota/metrics/prometheus"
	"github.com/mihaimyh/goquota/storage/memory"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

func main() {
	// 1. Setup structured logging with Zerolog
	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	zlog := zerolog.New(output).With().Timestamp().Logger()
	logger := zerolog_adapter.NewLogger(&zlog)

	logger.Info("Starting observability example")

	// 2. Setup Prometheus metrics
	metrics := prometheus_adapter.DefaultMetrics("goquota_example")

	// 3. Setup Manager with everything enabled
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"requests": 10},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled: true,
		},
		Metrics: metrics,
		Logger:  logger,
	}

	storage := memory.New()
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		logger.Error("Failed to create manager", goquota.Field{"error", err})
		os.Exit(1)
	}

	// 4. Start Prometheus metrics server in background
	go func() {
		fmt.Println("Prometheus metrics available at http://localhost:8080/metrics")
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logger.Error("Metrics server failed", goquota.Field{"error", err})
		}
	}()

	// 5. Generate some activity
	ctx := context.Background()
	userID := "user_obs"

	logger.Info("Generating quota activity...")
	for i := 1; i <= 15; i++ {
		_, err := manager.Consume(ctx, userID, "requests", 1, goquota.PeriodTypeMonthly)
		if err != nil {
			logger.Warn("Consumption failed", goquota.Field{"error", err}, goquota.Field{"attempt", i})
		} else {
			logger.Info("Consumption successful", goquota.Field{"attempt", i})
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Println("\n--- Example Finished ---")
	fmt.Println("Check the logs above for structured output.")
	fmt.Println("You can also visit http://localhost:8080/metrics to see the recorded metrics.")
	fmt.Println("Press Ctrl+C to exit.")

	// Keep the process alive so the user can check /metrics
	select {}
}
