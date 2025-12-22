package goquota

import (
	"context"
	"errors"
	"sync"
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
		_ = cb.Execute(ctx, func() error { return errors.New("fail") })
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

// Phase 3.3: Circuit Breaker Concurrency Tests

func TestCircuitBreaker_ConcurrentStateChanges(t *testing.T) {
	threshold := 3
	timeout := 100 * time.Millisecond
	cb := NewDefaultCircuitBreaker(threshold, timeout, nil)

	const goroutines = 50
	errChan := make(chan error, goroutines)

	// Concurrent Success and Failure calls
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			if id%2 == 0 {
				cb.Success()
			} else {
				cb.Failure(errors.New("test error"))
			}
			errChan <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent state change %d failed: %v", i, err)
		}
	}

	// Verify circuit is in a valid state
	state := cb.State()
	if state != StateClosed && state != StateOpen && state != StateHalfOpen {
		t.Errorf("Invalid circuit state: %v", state)
	}
}

func TestCircuitBreaker_ConcurrentExecute(t *testing.T) {
	threshold := 3
	timeout := 100 * time.Millisecond
	cb := NewDefaultCircuitBreaker(threshold, timeout, nil)

	ctx := context.Background()
	const goroutines = 100
	errChan := make(chan error, goroutines)

	// Concurrent Execute calls
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			err := cb.Execute(ctx, func() error {
				if id%10 == 0 {
					return errors.New("test error")
				}
				return nil
			})
			errChan <- err
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		err := <-errChan
		// Some may fail due to errors, some may fail due to circuit open
		// But all should return an error or nil, not panic
		if err != nil && !errors.Is(err, ErrCircuitOpen) && err.Error() != "test error" {
			t.Errorf("Unexpected error from concurrent Execute %d: %v", i, err)
		}
	}
}

func TestCircuitBreaker_StateChangeCallbackRace(t *testing.T) {
	threshold := 2
	timeout := 100 * time.Millisecond
	callCount := 0
	var mu sync.Mutex

	cb := NewDefaultCircuitBreaker(threshold, timeout, func(_ CircuitBreakerState) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	const goroutines = 50
	errChan := make(chan error, goroutines)

	// Concurrent operations that trigger state changes
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			if id%2 == 0 {
				cb.Failure(errors.New("test error"))
			} else {
				cb.Success()
			}
			errChan <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent operation %d failed: %v", i, err)
		}
	}

	// Verify callback was called (at least once)
	mu.Lock()
	count := callCount
	mu.Unlock()

	if count == 0 {
		t.Error("State change callback was never called")
	}
}

// Phase 6: State Transition Tests

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	threshold := 2
	timeout := 100 * time.Millisecond
	cb := NewDefaultCircuitBreaker(threshold, timeout, nil)

	ctx := context.Background()

	// Open the circuit
	for i := 0; i < threshold; i++ {
		_ = cb.Execute(ctx, func() error {
			return errors.New("fail")
		})
	}
	assert.Equal(t, StateOpen, cb.State())

	// Wait for reset timeout to go to half-open
	time.Sleep(timeout + 10*time.Millisecond)
	assert.Equal(t, StateHalfOpen, cb.State())

	// Failure in half-open should re-open the circuit
	err := cb.Execute(ctx, func() error {
		return errors.New("fail")
	})
	assert.Error(t, err)
	assert.Equal(t, StateOpen, cb.State())
}

func TestCircuitBreaker_ResetTimeoutEdge(t *testing.T) {
	threshold := 2
	timeout := 100 * time.Millisecond
	cb := NewDefaultCircuitBreaker(threshold, timeout, nil)

	ctx := context.Background()

	// Open the circuit
	for i := 0; i < threshold; i++ {
		_ = cb.Execute(ctx, func() error {
			return errors.New("fail")
		})
	}
	assert.Equal(t, StateOpen, cb.State())

	// Wait exactly at timeout boundary
	time.Sleep(timeout)

	// Should transition to half-open
	assert.Equal(t, StateHalfOpen, cb.State())
}

func TestCircuitBreaker_ConcurrentStateRead(t *testing.T) {
	threshold := 2
	timeout := 100 * time.Millisecond
	cb := NewDefaultCircuitBreaker(threshold, timeout, nil)

	ctx := context.Background()
	const goroutines = 50
	stateChan := make(chan CircuitBreakerState, goroutines)

	// Open the circuit
	for i := 0; i < threshold; i++ {
		_ = cb.Execute(ctx, func() error {
			return errors.New("fail")
		})
	}

	// Concurrent state reads while state is changing
	for i := 0; i < goroutines; i++ {
		go func() {
			stateChan <- cb.State()
		}()
	}

	// Collect states
	states := make(map[CircuitBreakerState]int)
	for i := 0; i < goroutines; i++ {
		state := <-stateChan
		states[state]++
	}

	// All states should be valid
	for state := range states {
		if state != StateClosed && state != StateOpen && state != StateHalfOpen {
			t.Errorf("Invalid state read: %v", state)
		}
	}
}
