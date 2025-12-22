package prommetrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Phase 8.2: Metrics Tests

func TestPrometheusMetrics_NewMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	if metrics == nil {
		t.Fatal("NewMetrics returned nil")
	}
}

func TestPrometheusMetrics_RecordConsumption(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record successful consumption
	metrics.RecordConsumption("user1", "api_calls", "scholar", 100, true)

	// Record failed consumption
	metrics.RecordConsumption("user1", "api_calls", "scholar", 200, false)

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected metrics to be recorded")
	}
}

func TestPrometheusMetrics_RecordQuotaCheck(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record quota check duration
	metrics.RecordQuotaCheck("user1", "api_calls", 50*time.Millisecond)

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected quota check metrics to be recorded")
	}
}

func TestPrometheusMetrics_RecordCacheHit(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record cache hit
	metrics.RecordCacheHit("entitlement")
	metrics.RecordCacheHit("usage")

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected cache hit metrics to be recorded")
	}
}

func TestPrometheusMetrics_RecordCacheMiss(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record cache miss
	metrics.RecordCacheMiss("entitlement")
	metrics.RecordCacheMiss("usage")

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected cache miss metrics to be recorded")
	}
}

func TestPrometheusMetrics_RecordStorageOperation(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record successful storage operation
	metrics.RecordStorageOperation("GetUsage", 10*time.Millisecond, nil)

	// Record failed storage operation
	metrics.RecordStorageOperation("ConsumeQuota", 20*time.Millisecond, errors.New("storage error"))

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected storage operation metrics to be recorded")
	}
}

func TestPrometheusMetrics_RecordCircuitBreakerStateChange(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record circuit breaker state changes
	metrics.RecordCircuitBreakerStateChange("open")
	metrics.RecordCircuitBreakerStateChange("closed")
	metrics.RecordCircuitBreakerStateChange("half_open")

	// Verify metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	if len(metric) == 0 {
		t.Error("Expected circuit breaker metrics to be recorded")
	}
}

func TestPrometheusMetrics_DefaultMetrics(t *testing.T) {
	metrics := DefaultMetrics("test_default")

	if metrics == nil {
		t.Fatal("DefaultMetrics returned nil")
	}

	// Verify it works
	metrics.RecordConsumption("user1", "api_calls", "scholar", 100, true)
	metrics.RecordCacheHit("entitlement")
	metrics.RecordCircuitBreakerStateChange("open")
}

func TestPrometheusMetrics_MultipleOperations(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record various operations
	metrics.RecordConsumption("user1", "api_calls", "scholar", 100, true)
	metrics.RecordConsumption("user2", "audio_seconds", "fluent", 500, true)
	metrics.RecordQuotaCheck("user1", "api_calls", 5*time.Millisecond)
	metrics.RecordCacheHit("entitlement")
	metrics.RecordCacheMiss("usage")
	metrics.RecordStorageOperation("ConsumeQuota", 10*time.Millisecond, nil)
	metrics.RecordCircuitBreakerStateChange("open")

	// Verify all metrics were recorded
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	// Should have multiple metric families
	if len(metric) < 5 {
		t.Errorf("Expected at least 5 metric families, got %d", len(metric))
	}
}

func TestPrometheusMetrics_ConsumptionLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test")

	// Record consumption with different labels
	metrics.RecordConsumption("user1", "api_calls", "scholar", 100, true)
	metrics.RecordConsumption("user1", "api_calls", "fluent", 200, true)
	metrics.RecordConsumption("user1", "audio_seconds", "scholar", 50, false)

	// Verify metrics were recorded with correct labels
	metric, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}

	// Find consumption metric
	var consumptionMetric *dto.MetricFamily
	for _, m := range metric {
		if m.GetName() == "test_quota_consumption_total" {
			consumptionMetric = m
			break
		}
	}

	if consumptionMetric == nil {
		t.Fatal("Expected to find consumption metric")
	}

	// Verify multiple time series (different label combinations)
	if len(consumptionMetric.Metric) < 3 {
		t.Errorf("Expected at least 3 time series, got %d", len(consumptionMetric.Metric))
	}
}
