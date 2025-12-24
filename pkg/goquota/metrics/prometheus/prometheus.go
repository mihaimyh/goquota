package prommetrics

import (
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/mihaimyh/goquota/pkg/goquota"
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

	// Usage API metrics
	usageAPIRequestsTotal               *prometheus.CounterVec
	usageAPIRequestDuration             *prometheus.HistogramVec
	usageAPIResourcesDiscovered         *prometheus.HistogramVec
	usageAPIResourceFilterEffectiveness *prometheus.GaugeVec

	// Forever credits metrics
	foreverCreditsBalance           *prometheus.GaugeVec
	foreverCreditsConsumptionTotal  *prometheus.CounterVec
	foreverCreditsConsumptionAmount *prometheus.HistogramVec
	orphanedForeverCreditsTotal     *prometheus.CounterVec
	hybridBillingUsersTotal         *prometheus.GaugeVec

	// Quota health metrics
	quotaWarningsTotal         *prometheus.CounterVec
	quotaExhaustionEventsTotal *prometheus.CounterVec
	quotaRefundsTotal          *prometheus.CounterVec
	quotaRefundAmount          *prometheus.HistogramVec
	idempotencyHitsTotal       *prometheus.CounterVec
	activeUsersByTier          *prometheus.GaugeVec
	usersApproachingLimit      *prometheus.GaugeVec

	// Performance optimization metrics
	resourceFilterQueriesSavedTotal  *prometheus.CounterVec
	resourceFilterEffectivenessRatio *prometheus.GaugeVec
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

		// Usage API metrics
		usageAPIRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "usage_api_requests_total",
			Help:      "Total number of Usage API requests.",
		}, []string{"status", "error_type"}),

		usageAPIRequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "usage_api_request_duration_seconds",
			Help:      "Latency of Usage API requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{}),

		usageAPIResourcesDiscovered: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "usage_api_resources_discovered",
			Help:      "Number of resources discovered per Usage API request.",
			Buckets:   []float64{1, 2, 5, 10, 20, 50, 100},
		}, []string{}),

		usageAPIResourceFilterEffectiveness: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "usage_api_resource_filter_effectiveness_ratio",
			Help:      "ResourceFilter effectiveness ratio (filtered/total).",
		}, []string{}),

		// Forever credits metrics
		foreverCreditsBalance: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "forever_credits_balance",
			Help:      "Current balance of forever credits.",
		}, []string{"resource", "tier"}),

		foreverCreditsConsumptionTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "forever_credits_consumption_total",
			Help:      "Total number of forever credits consumption attempts.",
		}, []string{"resource", "tier", "success"}),

		foreverCreditsConsumptionAmount: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "forever_credits_consumption_amount",
			Help:      "Distribution of forever credits consumption amounts.",
			Buckets:   []float64{1, 5, 10, 50, 100, 500, 1000},
		}, []string{"resource", "tier"}),

		orphanedForeverCreditsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "orphaned_forever_credits_total",
			Help:      "Total number of orphaned forever credits (not in current tier).",
		}, []string{"resource"}),

		hybridBillingUsersTotal: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "hybrid_billing_users_total",
			Help:      "Number of users with both monthly and forever credits active.",
		}, []string{}),

		// Quota health metrics
		quotaWarningsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quota_warnings_total",
			Help:      "Total number of quota warning events.",
		}, []string{"resource", "tier", "threshold"}),

		quotaExhaustionEventsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quota_exhaustion_events_total",
			Help:      "Total number of quota exhaustion events.",
		}, []string{"resource", "tier", "period_type"}),

		quotaRefundsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quota_refunds_total",
			Help:      "Total number of quota refunds.",
		}, []string{"resource", "reason"}),

		quotaRefundAmount: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "quota_refund_amount",
			Help:      "Distribution of quota refund amounts.",
			Buckets:   []float64{1, 5, 10, 50, 100, 500, 1000},
		}, []string{"resource"}),

		idempotencyHitsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "idempotency_hits_total",
			Help:      "Total number of idempotency hits (duplicate requests prevented).",
		}, []string{"operation_type"}),

		activeUsersByTier: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_users_by_tier",
			Help:      "Number of active users per tier.",
		}, []string{"tier"}),

		usersApproachingLimit: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "users_approaching_limit",
			Help:      "Number of users approaching their quota limits.",
		}, []string{"resource", "tier", "threshold_range"}),

		// Performance optimization metrics
		resourceFilterQueriesSavedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "resource_filter_queries_saved_total",
			Help:      "Total number of queries saved by ResourceFilter.",
		}, []string{}),

		resourceFilterEffectivenessRatio: factory.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "resource_filter_effectiveness_ratio",
			Help:      "ResourceFilter effectiveness ratio (filtered/total).",
		}, []string{}),
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

// Usage API metrics
func (m *Metrics) RecordUsageAPIRequest(status, errorType string) {
	if errorType == "" {
		errorType = "none"
	}
	m.usageAPIRequestsTotal.WithLabelValues(status, errorType).Inc()
}

func (m *Metrics) RecordUsageAPIRequestDuration(duration time.Duration) {
	m.usageAPIRequestDuration.WithLabelValues().Observe(duration.Seconds())
}

func (m *Metrics) RecordUsageAPIResourcesDiscovered(count int) {
	m.usageAPIResourcesDiscovered.WithLabelValues().Observe(float64(count))
}

func (m *Metrics) RecordUsageAPIResourceFilterEffectiveness(filteredCount, totalCount int) {
	if totalCount > 0 {
		ratio := float64(filteredCount) / float64(totalCount)
		m.usageAPIResourceFilterEffectiveness.WithLabelValues().Set(ratio)
	}
}

// Forever credits metrics
func (m *Metrics) RecordForeverCreditsBalance(resource, tier string, balance int) {
	m.foreverCreditsBalance.WithLabelValues(resource, tier).Set(float64(balance))
}

func (m *Metrics) RecordForeverCreditsConsumption(resource, tier string, success bool) {
	m.foreverCreditsConsumptionTotal.WithLabelValues(resource, tier, strconv.FormatBool(success)).Inc()
}

func (m *Metrics) RecordForeverCreditsConsumptionAmount(resource, tier string, amount int) {
	m.foreverCreditsConsumptionAmount.WithLabelValues(resource, tier).Observe(float64(amount))
}

func (m *Metrics) RecordOrphanedForeverCredits(_, resource string) {
	m.orphanedForeverCreditsTotal.WithLabelValues(resource).Inc()
}

func (m *Metrics) RecordHybridBillingUser(_ string) {
	m.hybridBillingUsersTotal.WithLabelValues().Inc()
}

// Quota health metrics
func (m *Metrics) RecordQuotaWarning(resource, tier string, threshold float64) {
	thresholdStr := fmt.Sprintf("%.2f", threshold)
	m.quotaWarningsTotal.WithLabelValues(resource, tier, thresholdStr).Inc()
}

func (m *Metrics) RecordQuotaExhaustion(resource, tier string, periodType goquota.PeriodType) {
	m.quotaExhaustionEventsTotal.WithLabelValues(resource, tier, string(periodType)).Inc()
}

func (m *Metrics) RecordQuotaRefund(resource, reason string) {
	if reason == "" {
		reason = "unknown"
	}
	m.quotaRefundsTotal.WithLabelValues(resource, reason).Inc()
}

func (m *Metrics) RecordQuotaRefundAmount(resource string, amount int) {
	m.quotaRefundAmount.WithLabelValues(resource).Observe(float64(amount))
}

func (m *Metrics) RecordIdempotencyHit(operationType string) {
	m.idempotencyHitsTotal.WithLabelValues(operationType).Inc()
}

func (m *Metrics) RecordActiveUserByTier(tier string) {
	m.activeUsersByTier.WithLabelValues(tier).Inc()
}

func (m *Metrics) RecordUsersApproachingLimit(resource, tier, thresholdRange string) {
	m.usersApproachingLimit.WithLabelValues(resource, tier, thresholdRange).Inc()
}

// Performance optimization metrics
func (m *Metrics) RecordResourceFilterQueriesSaved(savedCount int) {
	m.resourceFilterQueriesSavedTotal.WithLabelValues().Add(float64(savedCount))
}

func (m *Metrics) RecordResourceFilterEffectivenessRatio(ratio float64) {
	m.resourceFilterEffectivenessRatio.WithLabelValues().Set(ratio)
}

// DefaultMetrics returns a Metrics implementation using the default Prometheus registerer.
func DefaultMetrics(namespace string) *Metrics {
	return NewMetrics(prometheus.DefaultRegisterer, namespace)
}
