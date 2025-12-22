package prommetrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics implements goquota.Metrics using Prometheus.
type Metrics struct {
	consumptionTotal           *prometheus.CounterVec
	consumptionAmount          *prometheus.HistogramVec
	quotaCheckDuration         *prometheus.HistogramVec
	cacheHitsTotal             *prometheus.CounterVec
	cacheMissesTotal           *prometheus.CounterVec
	storageOpsDuration         *prometheus.HistogramVec
	storageOpsErrors           *prometheus.CounterVec
	circuitBreakerStateChanges *prometheus.CounterVec
}

// NewMetrics creates a new Prometheus metrics implementation.
func NewMetrics(reg prometheus.Registerer, namespace string) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		consumptionTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quota_consumption_total",
			Help:      "Total number of quota consumption attempts.",
		}, []string{"resource", "tier", "success"}),

		consumptionAmount: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "quota_consumption_amount",
			Help:      "Distribution of quota consumption amounts.",
			Buckets:   []float64{1, 5, 10, 50, 100, 500, 1000},
		}, []string{"resource", "tier"}),

		quotaCheckDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "quota_check_duration_seconds",
			Help:      "Latency of quota checks.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"resource"}),

		cacheHitsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "cache_hits_total",
			Help:      "Total number of cache hits.",
		}, []string{"type"}),

		cacheMissesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "cache_misses_total",
			Help:      "Total number of cache misses.",
		}, []string{"type"}),

		storageOpsDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "storage_operation_duration_seconds",
			Help:      "Latency of storage operations.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"operation"}),

		storageOpsErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "storage_operation_errors_total",
			Help:      "Total number of storage operation errors.",
		}, []string{"operation"}),
		circuitBreakerStateChanges: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_state_changes_total",
			Help:      "Total number of circuit breaker state changes.",
		}, []string{"state"}),
	}
}

func (m *Metrics) RecordConsumption(_, resource, tier string, amount int, success bool) {
	m.consumptionTotal.WithLabelValues(resource, tier, strconv.FormatBool(success)).Inc()
	if success {
		m.consumptionAmount.WithLabelValues(resource, tier).Observe(float64(amount))
	}
}

func (m *Metrics) RecordQuotaCheck(_, resource string, duration time.Duration) {
	m.quotaCheckDuration.WithLabelValues(resource).Observe(duration.Seconds())
}

func (m *Metrics) RecordCacheHit(cacheType string) {
	m.cacheHitsTotal.WithLabelValues(cacheType).Inc()
}

func (m *Metrics) RecordCacheMiss(cacheType string) {
	m.cacheMissesTotal.WithLabelValues(cacheType).Inc()
}

func (m *Metrics) RecordStorageOperation(operation string, duration time.Duration, err error) {
	m.storageOpsDuration.WithLabelValues(operation).Observe(duration.Seconds())
	if err != nil {
		m.storageOpsErrors.WithLabelValues(operation).Inc()
	}
}

func (m *Metrics) RecordCircuitBreakerStateChange(state string) {
	m.circuitBreakerStateChanges.WithLabelValues(state).Inc()
}

// DefaultMetrics returns a Metrics implementation using the default Prometheus registerer.
func DefaultMetrics(namespace string) *Metrics {
	return NewMetrics(prometheus.DefaultRegisterer, namespace)
}
