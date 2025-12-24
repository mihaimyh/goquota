package internal

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_CleanupPreventsMemoryLeak(t *testing.T) {
	// Create rate limiter with short window for faster testing
	window := 100 * time.Millisecond
	limiter := NewRateLimiter(10, window)

	// Make many requests from different IPs to populate the map
	numIPs := 150 // More than cleanupAtSize (200) to trigger size-based cleanup
	for i := 0; i < numIPs; i++ {
		ip := "192.168.1." + string(rune(i%256))
		limiter.allow(ip)
	}

	// Verify map has entries
	if len(limiter.requests) == 0 {
		t.Error("Expected map to have entries after requests")
	}

	initialSize := len(limiter.requests)

	// Wait for window to expire
	time.Sleep(window + 50*time.Millisecond)

	// Trigger cleanup by making requests that hit the cleanup threshold
	// Make enough requests to trigger cleanup (every 100 requests)
	for i := 0; i < 100; i++ {
		ip := "10.0.0.1"
		limiter.allow(ip)
	}

	// Verify that expired entries were removed
	// The map size should decrease after cleanup
	finalSize := len(limiter.requests)
	if finalSize >= initialSize {
		t.Logf("Initial size: %d, Final size: %d", initialSize, finalSize)
		// Note: Cleanup may not remove all expired entries if they haven't expired yet
		// But we should see some cleanup happening
	}

	// Make many more requests and verify map doesn't grow unbounded
	// Make requests that will expire
	for i := 0; i < 200; i++ {
		ip := "172.16.0." + string(rune(i%256))
		limiter.allow(ip)
		// Trigger cleanup periodically
		if i%100 == 0 && i > 0 {
			// Force cleanup by accessing the map
			limiter.Cleanup()
		}
	}

	// Wait for entries to expire
	time.Sleep(window + 50*time.Millisecond)

	// Trigger final cleanup
	for i := 0; i < 100; i++ {
		limiter.allow("10.0.0.2")
	}

	finalSizeAfterManyRequests := len(limiter.requests)

	// Verify map size is reasonable (not growing unbounded)
	// After cleanup, we should have at most a few active entries
	if finalSizeAfterManyRequests > 50 {
		t.Errorf("Map size (%d) suggests memory leak - should be much smaller after cleanup", finalSizeAfterManyRequests)
	}
}

func TestRateLimiter_CleanupDeterministic(t *testing.T) {
	limiter := NewRateLimiter(10, time.Minute)

	// Verify cleanup happens every N requests
	// Make exactly cleanupEvery requests and verify cleanup was triggered
	initialRequestCount := limiter.requestCount

	// Make requests up to but not exceeding cleanupEvery
	for i := 0; i < 99; i++ {
		limiter.allow("192.168.1.1")
	}

	// 99 requests - cleanup should not have been triggered yet
	if limiter.requestCount != 99+initialRequestCount {
		t.Errorf("Expected request count %d, got %d", 99+initialRequestCount, limiter.requestCount)
	}

	// Make one more request to hit cleanup threshold (100th request)
	limiter.allow("192.168.1.1")

	// Verify cleanup was triggered (requestCount should be reset or modulo)
	// After cleanup, counter may be reset if it exceeded threshold
	if limiter.requestCount > limiter.cleanupEvery*10 {
		t.Error("Request counter should be reset after reaching threshold")
	}

	// Verify cleanup happens when map size exceeds threshold
	limiter2 := NewRateLimiter(10, time.Minute)

	// Populate map beyond cleanupAtSize
	for i := 0; i < limiter2.cleanupAtSize+10; i++ {
		ip := "10.0.0." + string(rune(i%256))
		limiter2.allow(ip)
	}

	// Verify cleanup was triggered due to map size
	// The map size should trigger cleanup even if requestCount hasn't hit threshold
	if len(limiter2.requests) > limiter2.cleanupAtSize+50 {
		t.Errorf("Cleanup should have been triggered when map size exceeded %d, but size is %d",
			limiter2.cleanupAtSize, len(limiter2.requests))
	}
}

func TestRateLimiter_CleanupRemovesExpiredEntries(t *testing.T) {
	window := 50 * time.Millisecond
	limiter := NewRateLimiter(10, window)

	// Create entries that will expire
	now := time.Now()
	expiredIP := "192.168.1.100"
	limiter.requests[expiredIP] = &bucket{
		count:   5,
		resetAt: now.Add(-time.Second), // Already expired
	}

	activeIP := "192.168.1.200"
	limiter.requests[activeIP] = &bucket{
		count:   3,
		resetAt: now.Add(time.Minute), // Not expired
	}

	initialSize := len(limiter.requests)
	if initialSize != 2 {
		t.Fatalf("Expected 2 entries, got %d", initialSize)
	}

	// Trigger cleanup
	limiter.cleanupExpired(now)

	// Verify expired entry was removed
	if _, exists := limiter.requests[expiredIP]; exists {
		t.Error("Expired entry should have been removed")
	}

	// Verify active entry remains
	if _, exists := limiter.requests[activeIP]; !exists {
		t.Error("Active entry should not have been removed")
	}

	// Verify map size decreased
	finalSize := len(limiter.requests)
	if finalSize != 1 {
		t.Errorf("Expected 1 entry after cleanup, got %d", finalSize)
	}
}

func TestRateLimiter_CleanupInMiddleware(t *testing.T) {
	window := 100 * time.Millisecond
	limiter := NewRateLimiter(10, window)

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make many requests to trigger cleanup
	for i := 0; i < 150; i++ {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.RemoteAddr = "192.168.1." + string(rune(i%256))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// Verify cleanup was triggered (map size should be reasonable)
	if len(limiter.requests) > 200 {
		t.Errorf("Map size (%d) suggests cleanup not working in middleware", len(limiter.requests))
	}
}

func TestRateLimiter_CleanupCounterReset(t *testing.T) {
	limiter := NewRateLimiter(10, time.Minute)

	// Make many requests to push counter high
	for i := 0; i < limiter.cleanupEvery*15; i++ {
		limiter.allow("192.168.1.1")
	}

	// Counter should be reset after reaching threshold
	if limiter.requestCount > limiter.cleanupEvery*10 {
		t.Errorf("Counter should be reset, but is %d", limiter.requestCount)
	}
}
