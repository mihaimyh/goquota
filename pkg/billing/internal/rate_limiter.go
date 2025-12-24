package internal

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter provides simple in-memory rate limiting for webhook endpoints
// Limits requests per IP address to prevent DDoS attacks
type RateLimiter struct {
	mu            sync.Mutex
	requests      map[string]*bucket
	limit         int           // max requests per window
	window        time.Duration // time window
	requestCount  int           // counter for deterministic cleanup
	cleanupEvery  int           // cleanup every N requests (default: 100)
	cleanupAtSize int           // cleanup when map size exceeds this (default: 200)
}

type bucket struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a new rate limiter with the specified limit and window
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests:      make(map[string]*bucket),
		limit:         limit,
		window:        window,
		requestCount:  0,
		cleanupEvery:  100, // Cleanup every 100 requests
		cleanupAtSize: 200, // Cleanup when map size exceeds 200
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Deterministic cleanup: Run every N requests or when map gets too large
	rl.requestCount++
	shouldCleanup := rl.requestCount%rl.cleanupEvery == 0 || len(rl.requests) > rl.cleanupAtSize
	if shouldCleanup {
		rl.cleanupExpired(now)
		// Reset counter after cleanup to avoid overflow
		if rl.requestCount >= rl.cleanupEvery*10 {
			rl.requestCount = 0
		}
	}

	b, exists := rl.requests[ip]

	if !exists || now.After(b.resetAt) {
		rl.requests[ip] = &bucket{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	if b.count >= rl.limit {
		return false
	}

	b.count++
	return true
}

// cleanupExpired removes expired entries from the requests map to prevent memory leaks.
// Should be called periodically (e.g., every 100 requests) to prevent unbounded growth.
func (rl *RateLimiter) cleanupExpired(now time.Time) {
	for ip, bucket := range rl.requests {
		if now.After(bucket.resetAt) {
			delete(rl.requests, ip)
		}
	}
}

// Cleanup removes all expired entries from the rate limiter.
// Can be called periodically (e.g., via a background goroutine) for proactive cleanup.
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.cleanupExpired(time.Now())
}

// Middleware wraps HTTP handler with rate limiting
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := GetClientIP(r)
		if !rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetClientIP extracts the client IP address from the request.
// Checks X-Forwarded-For header first (set by proxies/load balancers),
// then falls back to RemoteAddr.
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (set by proxies/load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take first IP in the chain
		if ip := strings.Split(xff, ",")[0]; ip != "" {
			return strings.TrimSpace(ip)
		}
	}
	// Fallback to RemoteAddr
	return r.RemoteAddr
}
