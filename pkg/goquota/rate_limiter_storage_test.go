package goquota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRateLimitStorage struct {
	checkRateLimitFunc      func(ctx context.Context, req *RateLimitRequest) (bool, int, time.Time, error)
	recordRateLimitFunc     func(ctx context.Context, req *RateLimitRequest) error
	checkRateLimitCallCount int
}

func (m *mockRateLimitStorage) GetEntitlement(_ context.Context, _ string) (*Entitlement, error) {
	return nil, nil
}

func (m *mockRateLimitStorage) SetEntitlement(_ context.Context, _ *Entitlement) error {
	return nil
}

func (m *mockRateLimitStorage) GetUsage(_ context.Context, _, _ string, _ Period) (*Usage, error) {
	return nil, nil
}

func (m *mockRateLimitStorage) ConsumeQuota(_ context.Context, _ *ConsumeRequest) (int, error) {
	return 0, nil
}

func (m *mockRateLimitStorage) ApplyTierChange(_ context.Context, _ *TierChangeRequest) error {
	return nil
}

func (m *mockRateLimitStorage) SetUsage(_ context.Context, _, _ string, _ *Usage, _ Period) error {
	return nil
}

func (m *mockRateLimitStorage) RefundQuota(_ context.Context, _ *RefundRequest) error {
	return nil
}

func (m *mockRateLimitStorage) GetRefundRecord(_ context.Context, _ string) (*RefundRecord, error) {
	return nil, nil
}

func (m *mockRateLimitStorage) GetConsumptionRecord(_ context.Context, _ string) (*ConsumptionRecord, error) {
	return nil, nil
}

//nolint:gocritic // Named return values would reduce readability here
func (m *mockRateLimitStorage) CheckRateLimit(
	ctx context.Context, req *RateLimitRequest,
) (bool, int, time.Time, error) {
	m.checkRateLimitCallCount++
	if m.checkRateLimitFunc != nil {
		return m.checkRateLimitFunc(ctx, req)
	}
	return true, 100, time.Now().Add(time.Hour), nil
}

func (m *mockRateLimitStorage) RecordRateLimitRequest(ctx context.Context, req *RateLimitRequest) error {
	if m.recordRateLimitFunc != nil {
		return m.recordRateLimitFunc(ctx, req)
	}
	return nil
}

func TestStorageRateLimiter_Allow_TokenBucket_Allowed(t *testing.T) {
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, req *RateLimitRequest) (bool, int, time.Time, error) {
			assert.Equal(t, "token_bucket", req.Algorithm)
			assert.Equal(t, 10, req.Rate)
			assert.Equal(t, time.Second, req.Window)
			assert.Equal(t, 20, req.Burst)
			return true, 15, time.Now().Add(time.Second), nil
		},
	}

	limiter := NewStorageRateLimiter(storage)
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
	assert.Equal(t, 15, info.Remaining)
	assert.Equal(t, 10, info.Limit)
}

func TestStorageRateLimiter_Allow_TokenBucket_Exceeded(t *testing.T) {
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
			return false, 0, time.Now().Add(time.Second), nil
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 0, info.Remaining)
}

func TestStorageRateLimiter_Allow_SlidingWindow_Allowed(t *testing.T) {
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, req *RateLimitRequest) (bool, int, time.Time, error) {
			assert.Equal(t, "sliding_window", req.Algorithm)
			return true, 5, time.Now().Add(time.Minute), nil
		},
		recordRateLimitFunc: func(_ context.Context, req *RateLimitRequest) error {
			assert.Equal(t, "sliding_window", req.Algorithm)
			return nil
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
		Burst:     0, // Not used for sliding window
	}

	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.NotNil(t, info)
	assert.Equal(t, 5, info.Remaining)
	assert.Equal(t, 1, storage.checkRateLimitCallCount)
}

func TestStorageRateLimiter_Allow_StorageError_GracefulDegradation(t *testing.T) {
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
			return false, 0, time.Time{}, errors.New("storage error")
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	// On storage error, should allow request (graceful degradation)
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed) // Allowed due to graceful degradation
	assert.NotNil(t, info)
	assert.Equal(t, 10, info.Remaining) // Default to full limit
}

func TestStorageRateLimiter_Allow_UnknownAlgorithm(t *testing.T) {
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
			return false, 0, time.Time{}, errors.New("unknown algorithm")
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "unknown",
		Rate:      10,
		Window:    time.Second,
	}

	// Should propagate error from storage
	allowed, info, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	assert.Error(t, err)
	assert.False(t, allowed)
	assert.Nil(t, info)
}

func TestStorageRateLimiter_Allow_SlidingWindow_RecordsRequest(t *testing.T) {
	recordCallCount := 0
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, req *RateLimitRequest) (bool, int, time.Time, error) {
			assert.Equal(t, "sliding_window", req.Algorithm)
			return true, 5, time.Now().Add(time.Minute), nil
		},
		recordRateLimitFunc: func(_ context.Context, req *RateLimitRequest) error {
			recordCallCount++
			assert.Equal(t, "sliding_window", req.Algorithm)
			return nil
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "sliding_window",
		Rate:      10,
		Window:    time.Minute,
	}

	allowed, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	// RecordRateLimitRequest should be called for sliding window
	assert.Equal(t, 1, recordCallCount)
}

func TestStorageRateLimiter_Allow_TokenBucket_DoesNotRecord(t *testing.T) {
	recordCallCount := 0
	storage := &mockRateLimitStorage{
		checkRateLimitFunc: func(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
			return true, 5, time.Now().Add(time.Second), nil
		},
		recordRateLimitFunc: func(_ context.Context, _ *RateLimitRequest) error {
			recordCallCount++
			return nil
		},
	}

	limiter := NewStorageRateLimiter(storage)
	config := RateLimitConfig{
		Algorithm: "token_bucket",
		Rate:      10,
		Window:    time.Second,
		Burst:     20,
	}

	allowed, _, err := limiter.Allow(context.Background(), "user1", "api_calls", config)
	require.NoError(t, err)
	assert.True(t, allowed)
	// RecordRateLimitRequest should NOT be called for token bucket
	assert.Equal(t, 0, recordCallCount)
}
