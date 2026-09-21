package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestStorage_CheckRateLimit_TokenBucket_Allowed(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user1",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
		Now:       time.Now().UTC(),
	}

	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, remaining, 0)
	assert.False(t, resetTime.IsZero())
}

func TestStorage_CheckRateLimit_TokenBucket_Exceeded(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user2",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
		Now:       time.Now().UTC(),
	}

	// Consume all tokens
	for i := 0; i < 10; i++ {
		req.Now = time.Now().UTC()
		allowed, _, _, err := storage.CheckRateLimit(ctx, req)
		require.NoError(t, err)
		if i < 9 {
			assert.True(t, allowed, "should be allowed for request %d", i)
		}
	}

	// 11th request should be denied
	req.Now = time.Now().UTC()
	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, 0, remaining)
	assert.False(t, resetTime.IsZero())
}

func TestStorage_CheckRateLimit_TokenBucket_Refill(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user3",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
		Now:       time.Now().UTC(),
	}

	// Consume all tokens
	for i := 0; i < 10; i++ {
		req.Now = time.Now().UTC()
		_, _, _, err := storage.CheckRateLimit(ctx, req)
		require.NoError(t, err)
	}

	// Wait for refill
	time.Sleep(1100 * time.Millisecond)

	// Update Now time
	req.Now = time.Now().UTC()

	// Should be able to consume again
	allowed, remaining, _, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, remaining, 0)
}

func TestStorage_CheckRateLimit_SlidingWindow_Allowed(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user4",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Burst:     0,
		Now:       time.Now().UTC(),
	}

	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, remaining, 0)
	assert.False(t, resetTime.IsZero())
}

func TestStorage_CheckRateLimit_SlidingWindow_Exceeded(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user5",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Burst:     0,
		Now:       time.Now().UTC(),
	}

	// Make 10 requests
	for i := 0; i < 10; i++ {
		req.Now = time.Now().UTC()
		allowed, _, _, err := storage.CheckRateLimit(ctx, req)
		require.NoError(t, err)
		if i < 9 {
			assert.True(t, allowed, "should be allowed for request %d", i)
		}
	}

	// 11th request should be denied
	req.Now = time.Now().UTC()
	allowed, remaining, resetTime, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, 0, remaining)
	assert.False(t, resetTime.IsZero())
}

func TestStorage_CheckRateLimit_UnknownAlgorithm(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user6",
		Resource:  "api_calls",
		Algorithm: "unknown",
		Rate:      10,
		Window:    time.Second,
		Now:       time.Now().UTC(),
	}

	_, _, _, err := storage.CheckRateLimit(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown rate limit algorithm")
}

func TestStorage_RecordRateLimitRequest(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req := &goquota.RateLimitRequest{
		UserID:    "user7",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Now:       time.Now().UTC(),
	}

	// RecordRateLimitRequest is a no-op for Memory storage (handled in CheckRateLimit)
	err := storage.RecordRateLimitRequest(ctx, req)
	assert.NoError(t, err)
}

func TestStorage_CheckRateLimit_DifferentUsers(t *testing.T) {
	storage := New()
	ctx := context.Background()

	req1 := &goquota.RateLimitRequest{
		UserID:    "user8",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
		Now:       time.Now().UTC(),
	}

	req2 := &goquota.RateLimitRequest{
		UserID:    "user9",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     10,
		Now:       time.Now().UTC(),
	}

	// Consume all tokens for user8
	for i := 0; i < 10; i++ {
		req1.Now = time.Now().UTC()
		_, _, _, err := storage.CheckRateLimit(ctx, req1)
		require.NoError(t, err)
	}

	// user9 should still have tokens
	req2.Now = time.Now().UTC()
	allowed, remaining, _, err := storage.CheckRateLimit(ctx, req2)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Greater(t, remaining, 0)
}

// TestStorage_CheckRateLimit_SlidingWindow_ResetsAfterWindow is a regression
// test for STORAGE-2: once every timestamp in a sliding window has expired the
// limiter must reset, not remain permanently blocked.
func TestStorage_CheckRateLimit_SlidingWindow_ResetsAfterWindow(t *testing.T) {
	storage := New()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := 100 * time.Millisecond

	req := &goquota.RateLimitRequest{
		UserID:    "sliding_user",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      2,
		Window:    window,
		Now:       base,
	}

	// Fill the window.
	for i := 0; i < 2; i++ {
		allowed, _, _, err := storage.CheckRateLimit(ctx, req)
		require.NoError(t, err)
		require.True(t, allowed, "request %d should be allowed", i+1)
	}

	// A third request within the window is blocked.
	allowed, _, _, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	require.False(t, allowed, "third request should be rate limited")

	// Once the whole window has elapsed the limiter must allow requests again.
	req.Now = base.Add(window + time.Millisecond)
	allowed, _, _, err = storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.True(t, allowed, "sliding window did not reset after the window elapsed")
}

// TestStorage_CheckRateLimit_SlidingWindow_ZeroRateBlocks is a regression test
// for STORAGE-3: Rate=0 must block, not panic on an empty timestamp slice.
func TestStorage_CheckRateLimit_SlidingWindow_ZeroRateBlocks(t *testing.T) {
	storage := New()
	ctx := context.Background()
	req := &goquota.RateLimitRequest{
		UserID:    "zero_sliding",
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      0,
		Window:    time.Second,
		Now:       time.Now().UTC(),
	}
	allowed, _, _, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.False(t, allowed, "Rate=0 sliding window must always block")
}

// TestStorage_CheckRateLimit_TokenBucket_ZeroRateBlocks is a regression test for
// STORAGE-3: once the burst is drained at Rate=0, the refill math must not
// divide by zero.
func TestStorage_CheckRateLimit_TokenBucket_ZeroRateBlocks(t *testing.T) {
	storage := New()
	ctx := context.Background()
	req := &goquota.RateLimitRequest{
		UserID:    "zero_bucket",
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      0,
		Burst:     1,
		Window:    time.Second,
		Now:       time.Now().UTC(),
	}
	allowed, _, _, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	require.True(t, allowed, "first request consumes the burst token")
	allowed, _, _, err = storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.False(t, allowed, "Rate=0 token bucket must block once drained")
}

// TestStorage_CheckRateLimit_EvictsIdleKeys is a regression test for unbounded
// growth: idle rate-limit keys must be evicted periodically.
func TestStorage_CheckRateLimit_EvictsIdleKeys(t *testing.T) {
	store := New()
	ctx := context.Background()
	base := time.Now().UTC()
	req := &goquota.RateLimitRequest{
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      1000,
		Window:    10 * time.Millisecond,
		Burst:     1000,
		Now:       base,
	}

	for i := 0; i < 50; i++ {
		req.UserID = fmt.Sprintf("u%d", i)
		if _, _, _, err := store.CheckRateLimit(ctx, req); err != nil {
			t.Fatalf("rate limit %d: %v", i, err)
		}
	}
	if len(store.tokenBuckets) != 50 {
		t.Fatalf("expected 50 buckets, got %d", len(store.tokenBuckets))
	}

	// Advance beyond the full-refill duration (10ms) and trigger a sweep.
	req.UserID = "trigger"
	req.Now = base.Add(time.Second)
	for i := 0; i < 300; i++ {
		if _, _, _, err := store.CheckRateLimit(ctx, req); err != nil {
			t.Fatalf("trigger %d: %v", i, err)
		}
	}

	if len(store.tokenBuckets) > 5 {
		t.Fatalf("idle token buckets were not evicted: %d remain", len(store.tokenBuckets))
	}
}
