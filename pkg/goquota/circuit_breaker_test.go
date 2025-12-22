package goquota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultCircuitBreaker(t *testing.T) {
	threshold := 3
	timeout := 100 * time.Millisecond
	var lastState CircuitBreakerState
	cb := NewDefaultCircuitBreaker(threshold, timeout, func(state CircuitBreakerState) {
		lastState = state
	})

	ctx := context.Background()

	// Initial state: Closed
	assert.Equal(t, StateClosed, cb.State())

	// Record some failures
	for i := 0; i < threshold-1; i++ {
		err := cb.Execute(ctx, func() error {
			return errors.New("fail")
		})
		assert.Error(t, err)
		assert.Equal(t, StateClosed, cb.State())
	}

	// Next failure should open the circuit
	err := cb.Execute(ctx, func() error {
		return errors.New("fail")
	})
	assert.Error(t, err)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, StateOpen, lastState)

	// When open, Execute should fail fast
	err = cb.Execute(ctx, func() error {
		return nil
	})
	assert.ErrorIs(t, err, ErrCircuitOpen)

	// Wait for reset timeout
	time.Sleep(timeout + 10*time.Millisecond)

	// State should be Half-Open (via getter)
	assert.Equal(t, StateHalfOpen, cb.State())

	// Successful execute in half-open should close the circuit
	err = cb.Execute(ctx, func() error {
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, StateClosed, lastState)

	// Failure in half-open should re-open the circuit
	// First, let's open it again
	for i := 0; i < threshold; i++ {
		cb.Execute(ctx, func() error { return errors.New("fail") })
	}
	assert.Equal(t, StateOpen, cb.State())

	// Wait for reset timeout
	time.Sleep(timeout + 10*time.Millisecond)
	assert.Equal(t, StateHalfOpen, cb.State())

	// Fail in half-open
	err = cb.Execute(ctx, func() error {
		return errors.New("fail")
	})
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrCircuitOpen)
	assert.Equal(t, StateOpen, cb.State())
}
