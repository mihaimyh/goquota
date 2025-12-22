package goquota

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryRateLimiter_TokenBucket_Allowed(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 19, info.Remaining) // Burst - 1
	assert.Equal(t, 10, info.Limit)
}

func TestMemoryRateLimiter_TokenBucket_Exceeded(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	// Consume all tokens
	for i := 0; i < 20; i++ {
		allowed, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
		require.NoError(t, err)
		if i < 19 {
			assert.True(t, allowed, "should be allowed for request %d", i)
		}
	}

	// 21st request should be denied
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 0, info.Remaining)
}

func TestMemoryRateLimiter_TokenBucket_Refill(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	// Consume all tokens
	for i := 0; i < 20; i++ {
		_, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
		require.NoError(t, err)
	}

	// Wait for refill (should refill 10 tokens per second)
	time.Sleep(1100 * time.Millisecond)

	// Should be able to consume again
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, info.Remaining, 0)
}

func TestMemoryRateLimiter_TokenBucket_NoBurst(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     0, // Should default to Rate
	}

	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 9, info.Remaining) // Rate - 1
	assert.Equal(t, 10, info.Limit)
}

func TestMemoryRateLimiter_SlidingWindow_Allowed(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Burst:     0, // Not used
	}

	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 9, info.Remaining) // Rate - 1
	assert.Equal(t, 10, info.Limit)
}

func TestMemoryRateLimiter_SlidingWindow_Exceeded(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
	}

	// Make 10 requests (all should be allowed)
	for i := 0; i < 10; i++ {
		allowed, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
		require.NoError(t, err)
		assert.True(t, allowed, "should be allowed for request %d", i)
	}

	// 11th request should be denied
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 0, info.Remaining)
}

func TestMemoryRateLimiter_SlidingWindow_Expires(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    100 * time.Millisecond, // Short window for testing
	}

	// Make 10 requests
	for i := 0; i < 10; i++ {
		_, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
		require.NoError(t, err)
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Should be able to make requests again
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, info.Remaining, 0)
}

func TestMemoryRateLimiter_ConcurrentAccess(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      100,
		Window:    time.Second,
		Burst:     100,
	}

	var wg sync.WaitGroup
	allowedCount := 0
	var mu sync.Mutex

	// Make 200 concurrent requests
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
			require.NoError(t, err)
			if allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Should allow exactly burst amount (100)
	assert.Equal(t, 100, allowedCount)
}

func TestMemoryRateLimiter_DifferentUsers(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
	}

	// User1 consumes all tokens
	for i := 0; i < 10; i++ {
		_, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
		require.NoError(t, err)
	}

	// User2 should still have tokens
	allowed, info, err := limiter.Allow(context.Background(), "user2", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 9, info.Remaining)
}

func TestMemoryRateLimiter_DifferentResources(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
	}

	// Consume all tokens for resource1
	for i := 0; i < 10; i++ {
		_, _, err := limiter.Allow(context.Background(), "user1", "resource1", config)
		require.NoError(t, err)
	}

	// resource2 should still have tokens
	allowed, info, err := limiter.Allow(context.Background(), "user1", "resource2", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, 9, info.Remaining)
}

func TestMemoryRateLimiter_UnknownAlgorithm(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "unknown",
		Rate:      10,
		Window:    time.Second,
	}

	// Unknown algorithm should default to allowing
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.NotNil(t, info)
}

func TestMemoryRateLimiter_ZeroRate(t *testing.T) {
	limiter := NewMemoryRateLimiter()
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      0,
		Window:    time.Second,
		Burst:     0,
	}

	// Zero rate should deny immediately
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.NotNil(t, info)
}
