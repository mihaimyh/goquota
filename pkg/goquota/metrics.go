package goquota

import "time"

// Metrics defines the interface for tracking quota operations and performance.
type Metrics interface {
	// RecordConsumption records a quota consumption attempt.
	RecordConsumption(userID, resource, tier string, amount int, success bool)

	// RecordQuotaCheck records the duration of a quota check (Read).
	RecordQuotaCheck(userID, resource string, duration time.Duration)

	// RecordCacheHit records a cache hit for a specific cache type (e.g., "entitlement", "usage").
	RecordCacheHit(cacheType string)

	// RecordCacheMiss records a cache miss for a specific cache type.
	RecordCacheMiss(cacheType string)

	// RecordStorageOperation records the duration and status of a storage operation.
	RecordStorageOperation(operation string, duration time.Duration, err error)

	// RecordCircuitBreakerStateChange records a circuit breaker state change.
	RecordCircuitBreakerStateChange(state string)

	// RecordFallbackUsage records that fallback was used (trigger indicates why, e.g., "circuit_open", "storage_error")
	RecordFallbackUsage(trigger string)

	// RecordOptimisticConsumption records an optimistic quota consumption
	RecordOptimisticConsumption(amount int)

	// RecordFallbackHit records a successful fallback operation (strategy indicates which strategy succeeded)
	RecordFallbackHit(strategy string)

	// RecordRateLimitCheck records a rate limit check with duration
	RecordRateLimitCheck(userID, resource string, allowed bool, duration time.Duration)

	// RecordRateLimitExceeded records when a rate limit is exceeded
	RecordRateLimitExceeded(userID, resource string)

	// Usage API metrics
	// RecordUsageAPIRequest records a Usage API request
	RecordUsageAPIRequest(status string, errorType string)
	// RecordUsageAPIRequestDuration records the duration of a Usage API request
	RecordUsageAPIRequestDuration(duration time.Duration)
	// RecordUsageAPIResourcesDiscovered records the number of resources discovered per request
	RecordUsageAPIResourcesDiscovered(count int)
	// RecordUsageAPIResourceFilterEffectiveness records the ResourceFilter effectiveness ratio
	RecordUsageAPIResourceFilterEffectiveness(filteredCount, totalCount int)

	// Forever credits metrics
	// RecordForeverCreditsBalance records the current balance of forever credits
	RecordForeverCreditsBalance(resource, tier string, balance int)
	// RecordForeverCreditsConsumption records a forever credits consumption attempt
	RecordForeverCreditsConsumption(resource, tier string, success bool)
	// RecordForeverCreditsConsumptionAmount records the amount of forever credits consumed
	RecordForeverCreditsConsumptionAmount(resource, tier string, amount int)
	// RecordOrphanedForeverCredits records orphaned forever credits (not in current tier)
	RecordOrphanedForeverCredits(userID, resource string)
	// RecordHybridBillingUser records a user with both monthly and forever credits active
	RecordHybridBillingUser(userID string)

	// Quota health metrics
	// RecordQuotaWarning records a quota warning event
	RecordQuotaWarning(resource, tier string, threshold float64)
	// RecordQuotaExhaustion records a quota exhaustion event
	RecordQuotaExhaustion(resource, tier string, periodType PeriodType)
	// RecordQuotaRefund records a quota refund
	RecordQuotaRefund(resource, reason string)
	// RecordQuotaRefundAmount records the amount refunded
	RecordQuotaRefundAmount(resource string, amount int)
	// RecordIdempotencyHit records an idempotency hit (duplicate request prevented)
	RecordIdempotencyHit(operationType string)
	// RecordActiveUserByTier records an active user for a tier
	RecordActiveUserByTier(tier string)
	// RecordUsersApproachingLimit records users approaching their quota limits
	RecordUsersApproachingLimit(resource, tier string, thresholdRange string)

	// Performance optimization metrics
	// RecordResourceFilterQueriesSaved records queries saved by ResourceFilter
	RecordResourceFilterQueriesSaved(savedCount int)
	// RecordResourceFilterEffectivenessRatio records the effectiveness ratio
	RecordResourceFilterEffectivenessRatio(ratio float64)
}

// NoopMetrics is a no-op implementation of the Metrics interface.
type NoopMetrics struct{}

func (n *NoopMetrics) RecordConsumption(_, _, _ string, _ int, _ bool)           {}
func (n *NoopMetrics) RecordQuotaCheck(_, _ string, _ time.Duration)             {}
func (n *NoopMetrics) RecordCacheHit(_ string)                                   {}
func (n *NoopMetrics) RecordCacheMiss(_ string)                                  {}
func (n *NoopMetrics) RecordStorageOperation(_ string, _ time.Duration, _ error) {}
func (n *NoopMetrics) RecordCircuitBreakerStateChange(_ string)                  {}
func (n *NoopMetrics) RecordFallbackUsage(_ string)                              {}
func (n *NoopMetrics) RecordOptimisticConsumption(_ int)                         {}
func (n *NoopMetrics) RecordFallbackHit(_ string)                                {}
func (n *NoopMetrics) RecordRateLimitCheck(_, _ string, _ bool, _ time.Duration) {}
func (n *NoopMetrics) RecordRateLimitExceeded(_, _ string)                       {}
func (n *NoopMetrics) RecordUsageAPIRequest(_, _ string)                         {}
func (n *NoopMetrics) RecordUsageAPIRequestDuration(_ time.Duration)             {}
func (n *NoopMetrics) RecordUsageAPIResourcesDiscovered(_ int)                   {}
func (n *NoopMetrics) RecordUsageAPIResourceFilterEffectiveness(_, _ int)        {}
func (n *NoopMetrics) RecordForeverCreditsBalance(_, _ string, _ int)            {}
func (n *NoopMetrics) RecordForeverCreditsConsumption(_, _ string, _ bool)       {}
func (n *NoopMetrics) RecordForeverCreditsConsumptionAmount(_, _ string, _ int)  {}
func (n *NoopMetrics) RecordOrphanedForeverCredits(_, _ string)                  {}
func (n *NoopMetrics) RecordHybridBillingUser(_ string)                          {}
func (n *NoopMetrics) RecordQuotaWarning(_, _ string, _ float64)                 {}
func (n *NoopMetrics) RecordQuotaExhaustion(_, _ string, _ PeriodType)           {}
func (n *NoopMetrics) RecordQuotaRefund(_, _ string)                             {}
func (n *NoopMetrics) RecordQuotaRefundAmount(_ string, _ int)                   {}
func (n *NoopMetrics) RecordIdempotencyHit(_ string)                             {}
func (n *NoopMetrics) RecordActiveUserByTier(_ string)                           {}
func (n *NoopMetrics) RecordUsersApproachingLimit(_, _, _ string)                {}
func (n *NoopMetrics) RecordResourceFilterQueriesSaved(_ int)                    {}
func (n *NoopMetrics) RecordResourceFilterEffectivenessRatio(_ float64)          {}
