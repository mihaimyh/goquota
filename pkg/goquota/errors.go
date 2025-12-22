package goquota

import (
	"errors"
	"time"
)

var (
	// ErrQuotaExceeded is returned when quota limit is reached
	ErrQuotaExceeded = errors.New("quota exceeded")

	// ErrInvalidTier is returned for unknown tier
	ErrInvalidTier = errors.New("invalid tier")

	// ErrInvalidAmount is returned for negative amounts
	ErrInvalidAmount = errors.New("invalid amount")

	// ErrEntitlementNotFound is returned when user has no entitlement
	ErrEntitlementNotFound = errors.New("entitlement not found")

	// ErrStorageUnavailable is returned when storage is unavailable
	ErrStorageUnavailable = errors.New("storage unavailable")

	// ErrInvalidPeriod is returned for invalid period type
	ErrInvalidPeriod = errors.New("invalid period")

	// ErrFallbackUnavailable is returned when fallback strategy is unavailable
	ErrFallbackUnavailable = errors.New("fallback unavailable")

	// ErrStaleCache is returned when cached data is too stale for fallback
	ErrStaleCache = errors.New("stale cache")

	// ErrOptimisticLimitExceeded is returned when optimistic allowance limit is exceeded
	ErrOptimisticLimitExceeded = errors.New("optimistic limit exceeded")

	// ErrRateLimitExceeded is returned when rate limit is exceeded
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
)

// RateLimitExceededError provides detailed information about a rate limit exceeded error
type RateLimitExceededError struct {
	// Info contains rate limit information (remaining, reset time, limit)
	Info *RateLimitInfo

	// RetryAfter is the duration until the rate limit resets
	RetryAfter time.Duration
}

func (e *RateLimitExceededError) Error() string {
	return ErrRateLimitExceeded.Error()
}
