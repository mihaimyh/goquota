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
