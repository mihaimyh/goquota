package revenuecat

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter provides simple in-memory rate limiting for webhook endpoints
// Limits requests per IP address to prevent DDoS attacks
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string]*bucket
	limit    int           // max requests per window
	window   time.Duration // time window
}

type bucket struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string]*bucket),
		limit:    limit,
		window:   window,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Cleanup expired entries periodically (every 100 requests to avoid overhead)
	// This prevents memory leak from indefinite map growth
	if len(rl.requests) > 0 && len(rl.requests)%100 == 0 {
		rl.cleanupExpired(now)
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
func (rl *rateLimiter) cleanupExpired(now time.Time) {
	for ip, bucket := range rl.requests {
		if now.After(bucket.resetAt) {
			delete(rl.requests, ip)
		}
	}
}

// Cleanup removes all expired entries from the rate limiter.
// Can be called periodically (e.g., via a background goroutine) for proactive cleanup.
func (rl *rateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.cleanupExpired(time.Now())
}

// Middleware wraps HTTP handler with rate limiting
func (rl *rateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		if !rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getClientIP(r *http.Request) string {
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
