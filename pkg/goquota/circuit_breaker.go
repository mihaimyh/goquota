package goquota

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CircuitBreakerState represents the current state of the circuit breaker.
type CircuitBreakerState string

const (
	StateClosed   CircuitBreakerState = "closed"
	StateOpen     CircuitBreakerState = "open"
	StateHalfOpen CircuitBreakerState = "half_open"
)

var (
	// ErrCircuitOpen is returned when the circuit breaker is open.
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// CircuitBreaker defines the interface for a circuit breaker.
type CircuitBreaker interface {
	// Execute executes the given function within the circuit breaker.
	Execute(ctx context.Context, fn func() error) error
	// Success records a successful execution.
	Success()
	// Failure records a failed execution.
	Failure(err error)
	// State returns the current state of the circuit breaker.
	State() CircuitBreakerState
}

// DefaultCircuitBreaker is a simple circuit breaker implementation.
type DefaultCircuitBreaker struct {
	mu sync.RWMutex

	state               CircuitBreakerState
	failureThreshold    int
	resetTimeout        time.Duration
	consecutiveFailures int
	lastFailureTime     time.Time

	onStateChange func(state CircuitBreakerState)
}

// NewDefaultCircuitBreaker creates a new default circuit breaker.
func NewDefaultCircuitBreaker(failureThreshold int, resetTimeout time.Duration,
	onStateChange func(state CircuitBreakerState)) *DefaultCircuitBreaker {
	return &DefaultCircuitBreaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		onStateChange:    onStateChange,
	}
}

func (cb *DefaultCircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.currentState()
}

func (cb *DefaultCircuitBreaker) currentState() CircuitBreakerState {
	if cb.state == StateOpen && time.Since(cb.lastFailureTime) >= cb.resetTimeout {
		return StateHalfOpen
	}
	return cb.state
}

func (cb *DefaultCircuitBreaker) Execute(_ context.Context, fn func() error) error {
	state := cb.State()
	if state == StateOpen {
		return ErrCircuitOpen
	}

	err := fn()
	if err != nil {
		cb.Failure(err)
		return err
	}

	cb.Success()
	return nil
}

func (cb *DefaultCircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateHalfOpen || cb.state == StateOpen {
		cb.changeState(StateClosed)
	}
	cb.consecutiveFailures = 0
}

func (cb *DefaultCircuitBreaker) Failure(_ error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures++
	cb.lastFailureTime = time.Now()

	if cb.state == StateClosed && cb.consecutiveFailures >= cb.failureThreshold {
		cb.changeState(StateOpen)
	} else if cb.state == StateHalfOpen {
		cb.changeState(StateOpen)
	}
}

func (cb *DefaultCircuitBreaker) changeState(newState CircuitBreakerState) {
	if cb.state != newState {
		cb.state = newState
		if cb.onStateChange != nil {
			cb.onStateChange(newState)
		}
	}
}
