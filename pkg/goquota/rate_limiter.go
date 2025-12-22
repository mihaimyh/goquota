package goquota

import (
	"context"
)

// RateLimiter defines the interface for rate limiting implementations
type RateLimiter interface {
	// Allow checks if a request is allowed based on the rate limit
	// Returns (allowed, rateLimitInfo, error)
	// allowed: true if the request is allowed, false if rate limited
	// rateLimitInfo: information about the rate limit (remaining, reset time, limit)
	// error: any error that occurred during the check
	Allow(ctx context.Context, userID, resource string, config RateLimitConfig) (bool, *RateLimitInfo, error)
}

// NewRateLimiter creates a new rate limiter based on the provided configuration
// If storage is provided and useMemory is false, returns a storage-backed rate limiter
// If storage is nil or useMemory is true, returns an in-memory rate limiter
func NewRateLimiter(storage Storage, useMemory bool) RateLimiter {
	if storage != nil && !useMemory {
		return NewStorageRateLimiter(storage)
	}
	return NewMemoryRateLimiter()
}
