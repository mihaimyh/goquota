package goquota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CacheFallbackStrategy falls back to cached data when storage fails
type CacheFallbackStrategy struct {
	cache        Cache
	maxStaleness time.Duration
	metrics      Metrics
	logger       Logger
}

// NewCacheFallbackStrategy creates a new cache fallback strategy
func NewCacheFallbackStrategy(
	cache Cache,
	maxStaleness time.Duration,
	metrics Metrics,
	logger Logger,
) *CacheFallbackStrategy {
	return &CacheFallbackStrategy{
		cache:        cache,
		maxStaleness: maxStaleness,
		metrics:      metrics,
		logger:       logger,
	}
}

// ShouldFallback determines if error warrants fallback
func (s *CacheFallbackStrategy) ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	// Fallback on storage/circuit breaker errors, not business logic errors
	return errors.Is(err, ErrCircuitOpen) ||
		errors.Is(err, ErrStorageUnavailable) ||
		isContextError(err)
}

// GetFallbackUsage retrieves usage from cache if available and fresh enough
func (s *CacheFallbackStrategy) GetFallbackUsage(
	_ context.Context,
	userID, resource string,
	period Period,
) (*Usage, error) {
	if s.cache == nil {
		return nil, ErrFallbackUnavailable
	}

	usageKey := userID + ":" + resource + ":" + period.Key()
	usage, found := s.cache.GetUsage(usageKey)
	if !found {
		return nil, ErrFallbackUnavailable
	}

	// Check staleness
	if s.maxStaleness > 0 {
		age := time.Since(usage.UpdatedAt)
		if age > s.maxStaleness {
			s.logger.Warn("cache too stale for fallback",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"age", age},
				Field{"maxStaleness", s.maxStaleness},
			)
			return nil, ErrStaleCache
		}
	}

	s.metrics.RecordFallbackHit("cache")
	s.logger.Info("using cached usage for fallback",
		Field{"userId", userID},
		Field{"resource", resource},
	)

	return usage, nil
}

// GetFallbackEntitlement retrieves entitlement from cache if available and fresh enough
func (s *CacheFallbackStrategy) GetFallbackEntitlement(
	_ context.Context,
	userID string,
) (*Entitlement, error) {
	if s.cache == nil {
		return nil, ErrFallbackUnavailable
	}

	ent, found := s.cache.GetEntitlement(userID)
	if !found {
		return nil, ErrFallbackUnavailable
	}

	// Check staleness
	if s.maxStaleness > 0 {
		age := time.Since(ent.UpdatedAt)
		if age > s.maxStaleness {
			s.logger.Warn("cache too stale for fallback",
				Field{"userId", userID},
				Field{"age", age},
				Field{"maxStaleness", s.maxStaleness},
			)
			return nil, ErrStaleCache
		}
	}

	s.metrics.RecordFallbackHit("cache")
	s.logger.Info("using cached entitlement for fallback",
		Field{"userId", userID},
	)

	return ent, nil
}

// AllowOptimisticConsumption always returns false for cache fallback (not applicable)
func (s *CacheFallbackStrategy) AllowOptimisticConsumption(_ *Usage, _ int) bool {
	return false
}

// OptimisticFallbackStrategy allows quota consumption optimistically
type OptimisticFallbackStrategy struct {
	percentage      float64
	optimisticUsage map[string]int // key: userID:resource:periodKey, value: optimistic amount consumed
	mu              sync.RWMutex
	metrics         Metrics
	logger          Logger
}

// NewOptimisticFallbackStrategy creates a new optimistic fallback strategy
func NewOptimisticFallbackStrategy(percentage float64, metrics Metrics, logger Logger) *OptimisticFallbackStrategy {
	if percentage <= 0 {
		percentage = 10.0 // default 10%
	}
	if percentage > 100 {
		percentage = 100
	}

	return &OptimisticFallbackStrategy{
		percentage:      percentage,
		optimisticUsage: make(map[string]int),
		metrics:         metrics,
		logger:          logger,
	}
}

// ShouldFallback determines if error warrants fallback
func (s *OptimisticFallbackStrategy) ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrCircuitOpen) ||
		errors.Is(err, ErrStorageUnavailable) ||
		isContextError(err)
}

// GetFallbackUsage returns nil - optimistic strategy doesn't provide usage data
func (s *OptimisticFallbackStrategy) GetFallbackUsage(
	_ context.Context,
	_, _ string,
	_ Period,
) (*Usage, error) {
	return nil, ErrFallbackUnavailable
}

// GetFallbackEntitlement returns nil - optimistic strategy doesn't provide entitlement data
func (s *OptimisticFallbackStrategy) GetFallbackEntitlement(
	_ context.Context,
	_ string,
) (*Entitlement, error) {
	return nil, ErrFallbackUnavailable
}

// AllowOptimisticConsumption checks if optimistic consumption is allowed
func (s *OptimisticFallbackStrategy) AllowOptimisticConsumption(usage *Usage, amount int) bool {
	if usage == nil || amount <= 0 {
		return false
	}

	if usage.Limit <= 0 {
		return false
	}

	// Calculate maximum optimistic allowance
	maxOptimistic := int(float64(usage.Limit) * s.percentage / 100.0)
	if maxOptimistic <= 0 {
		return false
	}

	// Get current optimistic usage
	key := usage.UserID + ":" + usage.Resource + ":" + usage.Period.Key()
	s.mu.RLock()
	currentOptimistic := s.optimisticUsage[key]
	s.mu.RUnlock()

	// Check if adding this amount would exceed optimistic limit
	newOptimistic := currentOptimistic + amount
	if newOptimistic > maxOptimistic {
		s.logger.Warn("optimistic limit exceeded",
			Field{"userId", usage.UserID},
			Field{"resource", usage.Resource},
			Field{"currentOptimistic", currentOptimistic},
			Field{"requested", amount},
			Field{"maxOptimistic", maxOptimistic},
		)
		return false
	}

	// Track optimistic consumption
	s.mu.Lock()
	s.optimisticUsage[key] = newOptimistic
	s.mu.Unlock()

	s.metrics.RecordOptimisticConsumption(amount)
	s.metrics.RecordFallbackHit("optimistic")
	s.logger.Info("allowing optimistic consumption",
		Field{"userId", usage.UserID},
		Field{"resource", usage.Resource},
		Field{"amount", amount},
		Field{"totalOptimistic", newOptimistic},
		Field{"maxOptimistic", maxOptimistic},
	)

	return true
}

// ResetOptimisticUsage resets optimistic usage for a specific key (for testing/reconciliation)
func (s *OptimisticFallbackStrategy) ResetOptimisticUsage(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.optimisticUsage, key)
}

// GetOptimisticUsage returns current optimistic usage for a key (for testing/reconciliation)
func (s *OptimisticFallbackStrategy) GetOptimisticUsage(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.optimisticUsage[key]
}

// SecondaryStorageFallbackStrategy falls back to secondary storage
type SecondaryStorageFallbackStrategy struct {
	secondaryStorage Storage
	metrics          Metrics
	logger           Logger
}

// NewSecondaryStorageFallbackStrategy creates a new secondary storage fallback strategy
func NewSecondaryStorageFallbackStrategy(
	secondaryStorage Storage,
	metrics Metrics,
	logger Logger,
) *SecondaryStorageFallbackStrategy {
	return &SecondaryStorageFallbackStrategy{
		secondaryStorage: secondaryStorage,
		metrics:          metrics,
		logger:           logger,
	}
}

// ShouldFallback determines if error warrants fallback
func (s *SecondaryStorageFallbackStrategy) ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrCircuitOpen) ||
		errors.Is(err, ErrStorageUnavailable) ||
		isContextError(err)
}

// GetFallbackUsage retrieves usage from secondary storage
func (s *SecondaryStorageFallbackStrategy) GetFallbackUsage(
	ctx context.Context,
	userID, resource string,
	period Period,
) (*Usage, error) {
	if s.secondaryStorage == nil {
		return nil, ErrFallbackUnavailable
	}

	usage, err := s.secondaryStorage.GetUsage(ctx, userID, resource, period)
	if err != nil {
		s.logger.Warn("secondary storage failed to get usage",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"error", err},
		)
		return nil, fmt.Errorf("%w: %v", ErrFallbackUnavailable, err)
	}

	s.metrics.RecordFallbackHit("secondary_storage")
	s.logger.Info("using secondary storage for fallback",
		Field{"userId", userID},
		Field{"resource", resource},
	)

	return usage, nil
}

// GetFallbackEntitlement retrieves entitlement from secondary storage
func (s *SecondaryStorageFallbackStrategy) GetFallbackEntitlement(
	ctx context.Context,
	userID string,
) (*Entitlement, error) {
	if s.secondaryStorage == nil {
		return nil, ErrFallbackUnavailable
	}

	ent, err := s.secondaryStorage.GetEntitlement(ctx, userID)
	if err != nil {
		s.logger.Warn("secondary storage failed to get entitlement",
			Field{"userId", userID},
			Field{"error", err},
		)
		return nil, fmt.Errorf("%w: %v", ErrFallbackUnavailable, err)
	}

	s.metrics.RecordFallbackHit("secondary_storage")
	s.logger.Info("using secondary storage for fallback",
		Field{"userId", userID},
	)

	return ent, nil
}

// AllowOptimisticConsumption always returns false for secondary storage fallback (not applicable)
func (s *SecondaryStorageFallbackStrategy) AllowOptimisticConsumption(_ *Usage, _ int) bool {
	return false
}

// CompositeFallbackStrategy combines multiple fallback strategies
type CompositeFallbackStrategy struct {
	strategies []FallbackStrategy
	metrics    Metrics
	logger     Logger
}

// NewCompositeFallbackStrategy creates a new composite fallback strategy
func NewCompositeFallbackStrategy(
	strategies []FallbackStrategy,
	metrics Metrics,
	logger Logger,
) *CompositeFallbackStrategy {
	return &CompositeFallbackStrategy{
		strategies: strategies,
		metrics:    metrics,
		logger:     logger,
	}
}

// ShouldFallback determines if error warrants fallback (uses first strategy's logic)
func (s *CompositeFallbackStrategy) ShouldFallback(err error) bool {
	if len(s.strategies) == 0 {
		return false
	}
	return s.strategies[0].ShouldFallback(err)
}

// GetFallbackUsage tries each strategy in order until one succeeds
func (s *CompositeFallbackStrategy) GetFallbackUsage(
	ctx context.Context,
	userID, resource string,
	period Period,
) (*Usage, error) {
	var lastErr error
	for i, strategy := range s.strategies {
		var usage *Usage
		var err error
		// Recover from panics in strategies
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("panic in fallback strategy",
						Field{"strategy", i},
						Field{"panic", r},
					)
					err = ErrFallbackUnavailable
				}
			}()

			usage, err = strategy.GetFallbackUsage(ctx, userID, resource, period)
		}()

		if err == nil && usage != nil {
			return usage, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = ErrFallbackUnavailable
	}
	return nil, lastErr
}

// GetFallbackEntitlement tries each strategy in order until one succeeds
func (s *CompositeFallbackStrategy) GetFallbackEntitlement(ctx context.Context, userID string) (*Entitlement, error) {
	var lastErr error
	for i, strategy := range s.strategies {
		var ent *Entitlement
		var err error
		// Recover from panics in strategies
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("panic in fallback strategy",
						Field{"strategy", i},
						Field{"panic", r},
					)
					err = ErrFallbackUnavailable
				}
			}()

			ent, err = strategy.GetFallbackEntitlement(ctx, userID)
		}()

		if err == nil && ent != nil {
			return ent, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = ErrFallbackUnavailable
	}
	return nil, lastErr
}

// AllowOptimisticConsumption tries each strategy until one allows it
func (s *CompositeFallbackStrategy) AllowOptimisticConsumption(usage *Usage, amount int) bool {
	for i, strategy := range s.strategies {
		var allowed bool
		// Recover from panics in strategies
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("panic in fallback strategy",
						Field{"strategy", i},
						Field{"panic", r},
					)
					allowed = false
				}
			}()

			allowed = strategy.AllowOptimisticConsumption(usage, amount)
		}()

		if allowed {
			return true
		}
	}
	return false
}

// isContextError checks if error is a context error (timeout, cancellation)
func isContextError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
