package goquota

import (
	"context"
	"time"
)

// StorageRateLimiter implements rate limiting using the Storage interface
// This allows for distributed rate limiting across multiple instances
type StorageRateLimiter struct {
	storage Storage
}

// NewStorageRateLimiter creates a new storage-backed rate limiter
func NewStorageRateLimiter(storage Storage) *StorageRateLimiter {
	return &StorageRateLimiter{
		storage: storage,
	}
}

// Allow checks if a request is allowed based on the rate limit using storage
func (r *StorageRateLimiter) Allow(
	ctx context.Context, userID, resource string, config RateLimitConfig,
) (bool, *RateLimitInfo, error) {
	now := time.Now().UTC()

	req := &RateLimitRequest{
		UserID:    userID,
		Resource:  resource,
		Algorithm: config.Algorithm,
		Rate:      config.Rate,
		Window:    config.Window,
		Burst:     config.Burst,
		Now:       now,
	}

	allowed, remaining, resetTime, err := r.storage.CheckRateLimit(ctx, req)
	if err != nil {
		// On storage error, allow the request (graceful degradation)
		// This prevents rate limiting from blocking legitimate requests during storage outages
		return true, &RateLimitInfo{
			Remaining: config.Rate,
			ResetTime: now.Add(config.Window),
			Limit:     config.Rate,
		}, nil
	}

	// For sliding window, record the request if allowed
	if allowed && config.Algorithm == "sliding_window" {
		// Record the request timestamp for sliding window tracking
		// Ignore errors from recording - it's best effort
		//nolint:errcheck // Best-effort recording, errors are non-critical
		_ = r.storage.RecordRateLimitRequest(ctx, req)
	}

	info := &RateLimitInfo{
		Remaining: remaining,
		ResetTime: resetTime,
		Limit:     config.Rate,
	}

	return allowed, info, nil
}
