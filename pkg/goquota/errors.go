package goquota

import "errors"

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
)
