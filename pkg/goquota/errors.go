package goquota

import (
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// ErrIdempotencyKeyExists is returned when an idempotency key already exists
	// indicating the operation was already processed
	ErrIdempotencyKeyExists = errors.New("idempotency key already exists - operation already processed")

	// ErrUnsupportedOperation is returned when the storage backend cannot
	// implement the requested operation (for example MergeUser on Redis or tiered storage).
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrSameUser is returned when MergeUser source and target are the same identity.
	ErrSameUser = errors.New("source and target user are the same")

	// ErrUserSealed is returned when the identity has an active migration tombstone.
	ErrUserSealed = errors.New("user identity is sealed")

	// ErrInvalidMergeRequest is returned when MergeUser is missing required fields.
	ErrInvalidMergeRequest = errors.New("invalid merge request")

	// ErrInvalidPromotionRequest is returned when a promotion request is missing
	// required fields, targets an unconfigured tier, or has a non-future expiry.
	ErrInvalidPromotionRequest = errors.New("invalid promotion request")
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

// Unwrap exposes ErrRateLimitExceeded so that errors.Is(err, ErrRateLimitExceeded)
// matches a *RateLimitExceededError. Use errors.As when the details (Info,
// RetryAfter) are needed.
func (e *RateLimitExceededError) Unwrap() error {
	return ErrRateLimitExceeded
}

// GRPCStatus implements the interface understood by google.golang.org/grpc/status,
// so status.Code(err) returns codes.ResourceExhausted for a rate-limit denial
// instead of codes.Unknown.
func (e *RateLimitExceededError) GRPCStatus() *status.Status {
	return status.New(codes.ResourceExhausted, e.Error())
}

// GRPCStatusFor maps a quota or rate-limit denial to a gRPC status so servers
// can translate it without hand-written matching:
//
//	if st := goquota.GRPCStatusFor(err); st != nil {
//		return st.Err() // codes.ResourceExhausted
//	}
//
// It returns codes.ResourceExhausted for ErrQuotaExceeded, ErrRateLimitExceeded
// and ErrOptimisticLimitExceeded (including wrapped forms), propagates the
// status of errors that already implement GRPCStatus, and returns nil for any
// other error or for a nil error.
func GRPCStatusFor(err error) *status.Status {
	if err == nil {
		return nil
	}

	var withStatus interface {
		GRPCStatus() *status.Status
	}

	// Quota and rate-limit denials win first so that a wrapped denial keeps the
	// full error text, while a denial's own status still reports ResourceExhausted.
	if errors.Is(err, ErrQuotaExceeded) ||
		errors.Is(err, ErrRateLimitExceeded) ||
		errors.Is(err, ErrOptimisticLimitExceeded) {
		return status.New(codes.ResourceExhausted, err.Error())
	}

	// Any other error that already carries a gRPC status keeps it.
	if errors.As(err, &withStatus) {
		return withStatus.GRPCStatus()
	}

	return nil
}
