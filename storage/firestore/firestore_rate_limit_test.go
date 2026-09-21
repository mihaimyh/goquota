package firestore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Note: These tests require Firestore emulator to be running on localhost:8081
// The setupFirestoreClient helper automatically configures the connection

func TestStorage_CheckRateLimit_TokenBucket_Allowed(t *testing.T) {
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user1_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
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
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user2_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      10,
		// Long window so the 11 transactions below cannot refill a token
		// (refill = floor(rate*elapsed/window) = 0), keeping the denial
		// assertion deterministic under load.
		Window: time.Hour,
		Burst:  10,
		Now:    time.Now().UTC(),
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

func TestStorage_CheckRateLimit_SlidingWindow_Allowed(t *testing.T) {
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user3_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
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
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user4_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
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
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user5_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
		Resource:  "api_calls",
		Algorithm: "unknown",
		Rate:      10,
		Window:    time.Second,
		Now:       time.Now().UTC(),
	}

	_, _, _, err = storage.CheckRateLimit(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown rate limit algorithm")
}

func TestStorage_RecordRateLimitRequest(t *testing.T) {
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	// Use unique user ID to avoid conflicts between test runs
	userID := fmt.Sprintf("user6_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Now:       time.Now().UTC(),
	}

	// RecordRateLimitRequest is a no-op for Firestore (handled in CheckRateLimit)
	err = storage.RecordRateLimitRequest(ctx, req)
	assert.NoError(t, err)
}

// TestStorage_CheckRateLimit_TokenBucket_ZeroRateBlocks is a regression test for
// STORAGE-3 (Firestore half): Rate=0 must not divide by zero while computing the
// reset time; it must block once the burst is drained.
func TestStorage_CheckRateLimit_TokenBucket_ZeroRateBlocks(t *testing.T) {
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	userID := fmt.Sprintf("zero_bucket_%d", time.Now().UnixNano())
	req := &goquota.RateLimitRequest{
		UserID:    userID,
		Resource:  "api_calls",
		Algorithm: "token_bucket",
		Rate:      0,
		Burst:     1,
		Window:    time.Second,
		Now:       time.Now().UTC(),
	}

	allowed, _, _, err := storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	require.True(t, allowed, "first request consumes the single burst token")

	allowed, _, _, err = storage.CheckRateLimit(ctx, req)
	require.NoError(t, err)
	assert.False(t, allowed, "Rate=0 token bucket must block once drained")
}

// TestStorage_CheckRateLimit_SlidingWindow_CleansExpiredTimestamps is a
// regression test for the unbounded-growth finding: expired timestamp documents
// must be purged instead of accumulating forever.
func TestStorage_CheckRateLimit_SlidingWindow_CleansExpiredTimestamps(t *testing.T) {
	ctx := context.Background()
	client := setupFirestoreClient(t)
	defer client.Close()

	storage, err := New(client, Config{})
	require.NoError(t, err)

	userID := fmt.Sprintf("sl_clean_%d", time.Now().UnixNano())
	base := time.Now().UTC()
	window := time.Minute
	req := &goquota.RateLimitRequest{
		UserID:    userID,
		Resource:  "api_calls",
		Algorithm: "sliding_window",
		Rate:      100,
		Window:    window,
		Now:       base,
	}
	for i := 0; i < 20; i++ {
		if _, _, _, err := storage.CheckRateLimit(ctx, req); err != nil {
			t.Fatalf("rate limit %d: %v", i, err)
		}
	}

	// Advance past the window; the next check must purge the expired timestamps.
	req.Now = base.Add(window + time.Second)
	if _, _, _, err := storage.CheckRateLimit(ctx, req); err != nil {
		t.Fatalf("post-window rate limit: %v", err)
	}

	docs, err := client.Collection("rate_limits").
		Doc(fmt.Sprintf("%s_%s", userID, "api_calls")).
		Collection("timestamps").Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("count timestamps: %v", err)
	}
	if len(docs) > 5 {
		t.Fatalf("expired sliding-window timestamps were not cleaned: %d docs remain", len(docs))
	}
}
