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
	fallbackUsageTotal         *prometheus.CounterVec
	optimisticConsumptionTotal *prometheus.CounterVec
	fallbackHitsTotal          *prometheus.CounterVec
	rateLimitCheckDuration     *prometheus.HistogramVec
	rateLimitExceededTotal     *prometheus.CounterVec
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

		fallbackUsageTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "fallback_usage_total",
			Help:      "Total number of fallback usage events.",
		}, []string{"trigger"}),

		optimisticConsumptionTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "optimistic_consumption_total",
			Help:      "Total amount of quota consumed optimistically.",
		}, []string{}),

		fallbackHitsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "fallback_hits_total",
			Help:      "Total number of successful fallback operations.",
		}, []string{"strategy"}),

		rateLimitCheckDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "rate_limit_check_duration_seconds",
			Help:      "Latency of rate limit checks.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"resource"}),

		rateLimitExceededTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limit_exceeded_total",
			Help:      "Total number of rate limit exceeded events.",
		}, []string{"resource"}),
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

func (m *Metrics) RecordFallbackUsage(trigger string) {
	m.fallbackUsageTotal.WithLabelValues(trigger).Inc()
}

func (m *Metrics) RecordOptimisticConsumption(amount int) {
	m.optimisticConsumptionTotal.WithLabelValues().Add(float64(amount))
}

func (m *Metrics) RecordFallbackHit(strategy string) {
	m.fallbackHitsTotal.WithLabelValues(strategy).Inc()
}

func (m *Metrics) RecordRateLimitCheck(_, resource string, allowed bool, duration time.Duration) {
	m.rateLimitCheckDuration.WithLabelValues(resource).Observe(duration.Seconds())
	if !allowed {
		m.rateLimitExceededTotal.WithLabelValues(resource).Inc()
	}
}

func (m *Metrics) RecordRateLimitExceeded(_, resource string) {
	m.rateLimitExceededTotal.WithLabelValues(resource).Inc()
}

// DefaultMetrics returns a Metrics implementation using the default Prometheus registerer.
func DefaultMetrics(namespace string) *Metrics {
	return NewMetrics(prometheus.DefaultRegisterer, namespace)
}
