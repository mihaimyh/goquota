package goquota_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func newRateLimitError() *goquota.RateLimitExceededError {
	return &goquota.RateLimitExceededError{
		Info: &goquota.RateLimitInfo{
			Limit:     10,
			Remaining: 0,
			ResetTime: time.Now().Add(time.Minute),
		},
		RetryAfter: time.Minute,
	}
}

func TestRateLimitExceededError_IsMatchesSentinel(t *testing.T) {
	err := error(newRateLimitError())

	assert.True(t, errors.Is(err, goquota.ErrRateLimitExceeded),
		"errors.Is must match the sentinel via Unwrap")
	assert.False(t, errors.Is(err, goquota.ErrQuotaExceeded))

	// Typed details must stay reachable.
	var rateLimitErr *goquota.RateLimitExceededError
	require.True(t, errors.As(err, &rateLimitErr))
	assert.Equal(t, 10, rateLimitErr.Info.Limit)
	assert.Equal(t, time.Minute, rateLimitErr.RetryAfter)
}

func TestRateLimitExceededError_IsMatchesThroughWrapping(t *testing.T) {
	wrapped := errors.Join(errors.New("handler failed"), newRateLimitError())

	assert.True(t, errors.Is(wrapped, goquota.ErrRateLimitExceeded))
}

func TestRateLimitExceededError_GRPCStatus(t *testing.T) {
	err := newRateLimitError()

	// status.Code/FromError must see the ResourceExhausted status directly.
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Equal(t, goquota.ErrRateLimitExceeded.Error(), st.Message())
}

func TestGRPCStatusFor(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
		wantNil  bool
	}{
		{
			name:     "quota exceeded sentinel",
			err:      goquota.ErrQuotaExceeded,
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "wrapped quota exceeded",
			err:      errors.Join(errors.New("consume failed"), goquota.ErrQuotaExceeded),
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "rate limit exceeded sentinel",
			err:      goquota.ErrRateLimitExceeded,
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "typed rate limit error",
			err:      newRateLimitError(),
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "wrapped typed rate limit error",
			err:      errors.Join(errors.New("consume failed"), newRateLimitError()),
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "optimistic limit exceeded",
			err:      goquota.ErrOptimisticLimitExceeded,
			wantCode: codes.ResourceExhausted,
		},
		{
			name:    "nil error",
			err:     nil,
			wantNil: true,
		},
		{
			name:    "unrelated error",
			err:     goquota.ErrStorageUnavailable,
			wantNil: true,
		},
		{
			name:    "unrelated wrapped error",
			err:     errors.Join(errors.New("boom"), goquota.ErrInvalidTier),
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := goquota.GRPCStatusFor(tt.err)
			if tt.wantNil {
				assert.Nil(t, st)
				return
			}
			require.NotNil(t, st)
			assert.Equal(t, tt.wantCode, st.Code())
			assert.Equal(t, tt.err.Error(), st.Message())
		})
	}
}

func TestGRPCStatusFor_PreservesExistingStatus(t *testing.T) {
	original := status.New(codes.DeadlineExceeded, "upstream timeout")

	assert.Equal(t, original, goquota.GRPCStatusFor(original.Err()))
}
