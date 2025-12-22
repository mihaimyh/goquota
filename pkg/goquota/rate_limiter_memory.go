package goquota

import (
	"context"
	"sync"
	"time"
)

const (
	algorithmTokenBucket   = "token_bucket"
	algorithmSlidingWindow = "sliding_window"
)

// MemoryRateLimiter implements rate limiting using in-memory data structures
// This is useful for single-instance deployments or when storage is unavailable
type MemoryRateLimiter struct {
	mu sync.RWMutex
	// tokenBuckets stores token bucket state: key = userID:resource
	tokenBuckets map[string]*tokenBucketState
	// slidingWindows stores sliding window state: key = userID:resource
	slidingWindows map[string]*slidingWindowState
}

type tokenBucketState struct {
	mu         sync.Mutex
	tokens     int
	lastRefill time.Time
	capacity   int
	refillRate int // tokens per window
	window     time.Duration
}

type slidingWindowState struct {
	mu         sync.Mutex
	timestamps []time.Time
	window     time.Duration
	limit      int
}

// NewMemoryRateLimiter creates a new in-memory rate limiter
func NewMemoryRateLimiter() *MemoryRateLimiter {
	return &MemoryRateLimiter{
		tokenBuckets:   make(map[string]*tokenBucketState),
		slidingWindows: make(map[string]*slidingWindowState),
	}
}

// Allow checks if a request is allowed based on the rate limit
func (r *MemoryRateLimiter) Allow(
	_ context.Context, userID, resource string, config RateLimitConfig,
) (bool, *RateLimitInfo, error) {
	key := userID + ":" + resource
	now := time.Now().UTC()

	switch config.Algorithm {
	case algorithmTokenBucket:
		return r.allowTokenBucket(key, config, now)
	case algorithmSlidingWindow:
		return r.allowSlidingWindow(key, config, now)
	default:
		// Unknown algorithm, default to allowing
		return true, &RateLimitInfo{
			Remaining: config.Rate,
			ResetTime: now.Add(config.Window),
			Limit:     config.Rate,
		}, nil
	}
}

func (r *MemoryRateLimiter) allowTokenBucket(
	key string, config RateLimitConfig, now time.Time,
) (bool, *RateLimitInfo, error) {
	r.mu.Lock()
	bucket, exists := r.tokenBuckets[key]
	if !exists {
		capacity := config.Burst
		if capacity <= 0 {
			capacity = config.Rate
		}
		bucket = &tokenBucketState{
			tokens:     capacity,
			lastRefill: now,
			capacity:   capacity,
			refillRate: config.Rate,
			window:     config.Window,
		}
		r.tokenBuckets[key] = bucket
	}
	r.mu.Unlock()

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill tokens based on elapsed time
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed > 0 {
		tokensToAdd := int(float64(bucket.refillRate) * elapsed.Seconds() / bucket.window.Seconds())
		if tokensToAdd > 0 {
			bucket.tokens = intMin(bucket.tokens+tokensToAdd, bucket.capacity)
			bucket.lastRefill = now
		}
	}

	// Check if we have tokens available
	if bucket.tokens <= 0 {
		// Calculate reset time (when next token will be available)
		var nextTokenTime time.Time
		if bucket.refillRate > 0 {
			nextTokenTime = bucket.lastRefill.Add(bucket.window / time.Duration(bucket.refillRate))
			if nextTokenTime.Before(now) {
				nextTokenTime = now.Add(bucket.window / time.Duration(bucket.refillRate))
			}
		} else {
			// Zero rate means no tokens ever available
			nextTokenTime = now.Add(bucket.window)
		}
		return false, &RateLimitInfo{
			Remaining: 0,
			ResetTime: nextTokenTime,
			Limit:     config.Rate,
		}, nil
	}

	// Consume a token
	bucket.tokens--

	// Calculate reset time (when bucket will be full again)
	resetTime := now.Add(bucket.window)
	if bucket.tokens < bucket.capacity {
		// Calculate time until full
		tokensNeeded := bucket.capacity - bucket.tokens
		timeToFull := time.Duration(float64(tokensNeeded) * float64(bucket.window) / float64(bucket.refillRate))
		resetTime = now.Add(timeToFull)
	}

	return true, &RateLimitInfo{
		Remaining: bucket.tokens,
		ResetTime: resetTime,
		Limit:     config.Rate,
	}, nil
}

func (r *MemoryRateLimiter) allowSlidingWindow(
	key string, config RateLimitConfig, now time.Time,
) (bool, *RateLimitInfo, error) {
	r.mu.Lock()
	window, exists := r.slidingWindows[key]
	if !exists {
		window = &slidingWindowState{
			timestamps: make([]time.Time, 0),
			window:     config.Window,
			limit:      config.Rate,
		}
		r.slidingWindows[key] = window
	}
	r.mu.Unlock()

	window.mu.Lock()
	defer window.mu.Unlock()

	// Remove timestamps outside the window
	cutoff := now.Add(-window.window)
	validTimestamps := make([]time.Time, 0, len(window.timestamps))
	for _, ts := range window.timestamps {
		if ts.After(cutoff) {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	window.timestamps = validTimestamps

	// Check if we're within the limit
	if len(window.timestamps) >= window.limit {
		// Find the oldest timestamp still in the window
		oldestInWindow := window.timestamps[0]
		resetTime := oldestInWindow.Add(window.window)
		return false, &RateLimitInfo{
			Remaining: 0,
			ResetTime: resetTime,
			Limit:     config.Rate,
		}, nil
	}

	// Add current timestamp
	window.timestamps = append(window.timestamps, now)

	remaining := window.limit - len(window.timestamps)
	resetTime := now.Add(window.window)
	if len(window.timestamps) > 0 {
		// Reset time is when the oldest request expires
		oldest := window.timestamps[0]
		resetTime = oldest.Add(window.window)
	}

	return true, &RateLimitInfo{
		Remaining: remaining,
		ResetTime: resetTime,
		Limit:     config.Rate,
	}, nil
}

// intMin returns the minimum of two integers
func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
