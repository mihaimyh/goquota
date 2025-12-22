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
}

// NoopMetrics is a no-op implementation of the Metrics interface.
type NoopMetrics struct{}

func (n *NoopMetrics) RecordConsumption(userID, resource, tier string, amount int, success bool)  {}
func (n *NoopMetrics) RecordQuotaCheck(userID, resource string, duration time.Duration)           {}
func (n *NoopMetrics) RecordCacheHit(cacheType string)                                            {}
func (n *NoopMetrics) RecordCacheMiss(cacheType string)                                           {}
func (n *NoopMetrics) RecordStorageOperation(operation string, duration time.Duration, err error) {}
func (n *NoopMetrics) RecordCircuitBreakerStateChange(state string)                               {}
