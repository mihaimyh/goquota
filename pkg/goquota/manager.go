// Package goquota provides production-ready subscription quota management for Go applications.
//
// Features:
//   - Anniversary-based billing cycles with prorated tier changes
//   - Multiple quota types: daily, monthly, and forever (pre-paid credits)
//   - Pluggable storage: Redis (recommended), PostgreSQL, Firestore, In-Memory
//   - Rate limiting: Token bucket and sliding window algorithms
//   - Admin operations: SetUsage, GrantOneTimeCredit, ResetUsage
//   - Dry-run mode: Test quota rules without blocking traffic
//   - Audit trail: Comprehensive logging for compliance
//   - Clock skew protection: Uses storage server time for consistency
//   - Enhanced response: Detailed usage info without extra storage calls
//   - Config validation: Fail-fast on startup
//   - High performance: Redis adapter uses atomic Lua scripts for <1ms latency
//   - HTTP middlewares: Gin, Echo, Fiber, and standard net/http
//   - Billing integration: RevenueCat, Stripe webhook processing
//
// Quick Start:
//
//	storage := memory.New()
//	config := goquota.Config{
//	    DefaultTier: "free",
//	    Tiers: map[string]goquota.TierConfig{
//	        "free": {
//	            MonthlyQuotas: map[string]int{"api_calls": 100},
//	        },
//	    },
//	}
//	manager, _ := goquota.NewManager(storage, &config)
//	newUsed, err := manager.Consume(ctx, "user123", "api_calls", 1, goquota.PeriodTypeMonthly)
//
// See https://github.com/mihaimyh/goquota for complete documentation and examples.
package goquota

import (
	"context"
	"fmt"
	"math"
	"time"

	"golang.org/x/sync/singleflight"
)

type contextWarningKey struct{}

// WithWarningHandler returns a new context with a warning handler.
// This allows per-request warning handling (e.g. in middleware).
func WithWarningHandler(ctx context.Context, handler WarningHandler) context.Context {
	return context.WithValue(ctx, contextWarningKey{}, handler)
}

// Manager manages quota consumption and tracking across multiple resources and time periods
type Manager struct {
	storage          Storage
	timeSource       TimeSource // Optional: uses storage time if available, falls back to time.Now()
	config           Config
	cache            Cache
	metrics          Metrics
	logger           Logger
	fallbackStrategy FallbackStrategy
	rateLimiter      RateLimiter
	// singleflight groups to prevent cache stampede
	entitlementGroup singleflight.Group
	usageGroup       singleflight.Group
}

// NewManager creates a new quota manager with the given storage and configuration
func NewManager(storage Storage, config *Config) (*Manager, error) {
	if storage == nil {
		return nil, ErrStorageUnavailable
	}
	if config == nil {
		config = &Config{}
	}

	// Validate configuration before proceeding (fail fast)
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	applyConfigDefaults(config)
	cache := initializeCache(config)
	metrics := initializeMetrics(config.Metrics)
	logger := initializeLogger(config.Logger)
	currentStorage := initializeCircuitBreaker(storage, config.CircuitBreakerConfig, metrics)
	fallbackStrategy := initializeFallback(config, cache, metrics, logger)
	rateLimiter := NewRateLimiter(currentStorage, false) // Use storage-backed rate limiter

	// Check if storage implements TimeSource interface
	var timeSource TimeSource
	if ts, ok := currentStorage.(TimeSource); ok {
		timeSource = ts
		logger.Info("storage implements TimeSource, using storage engine time")
	} else {
		logger.Info("storage does not implement TimeSource, using application server time")
	}

	return &Manager{
		storage:          currentStorage,
		timeSource:       timeSource,
		config:           *config,
		cache:            cache,
		metrics:          metrics,
		logger:           logger,
		fallbackStrategy: fallbackStrategy,
		rateLimiter:      rateLimiter,
	}, nil
}

// applyConfigDefaults sets default values for config fields
func applyConfigDefaults(config *Config) {
	if config.CacheTTL == 0 {
		config.CacheTTL = time.Minute
	}
	if config.DefaultTier == "" {
		config.DefaultTier = "explorer"
	}
	if config.IdempotencyKeyTTL == 0 {
		config.IdempotencyKeyTTL = 24 * time.Hour
	}
}

// initializeCache creates and configures the cache based on config
func initializeCache(config *Config) Cache {
	if config.CacheConfig == nil || !config.CacheConfig.Enabled {
		return NewNoopCache()
	}

	// Set cache defaults
	cacheConfig := config.CacheConfig
	if cacheConfig.EntitlementTTL == 0 {
		cacheConfig.EntitlementTTL = time.Minute
	}
	if cacheConfig.UsageTTL == 0 {
		cacheConfig.UsageTTL = 10 * time.Second
	}
	if cacheConfig.MaxEntitlements == 0 {
		cacheConfig.MaxEntitlements = 1000
	}
	if cacheConfig.MaxUsage == 0 {
		cacheConfig.MaxUsage = 10000
	}

	return NewLRUCache(cacheConfig.MaxEntitlements, cacheConfig.MaxUsage)
}

// initializeMetrics returns the configured metrics or a no-op implementation
func initializeMetrics(metrics Metrics) Metrics {
	if metrics == nil {
		return &NoopMetrics{}
	}
	return metrics
}

// initializeLogger returns the configured logger or a no-op implementation
func initializeLogger(logger Logger) Logger {
	if logger == nil {
		return &NoopLogger{}
	}
	return logger
}

// initializeCircuitBreaker wraps storage with circuit breaker if enabled
func initializeCircuitBreaker(storage Storage, cbConfig *CircuitBreakerConfig, metrics Metrics) Storage {
	if cbConfig == nil || !cbConfig.Enabled {
		return storage
	}

	// Set circuit breaker defaults
	if cbConfig.FailureThreshold == 0 {
		cbConfig.FailureThreshold = 5
	}
	if cbConfig.ResetTimeout == 0 {
		cbConfig.ResetTimeout = 30 * time.Second
	}

	cb := NewDefaultCircuitBreaker(
		cbConfig.FailureThreshold,
		cbConfig.ResetTimeout,
		func(state CircuitBreakerState) {
			metrics.RecordCircuitBreakerStateChange(string(state))
		},
	)

	return NewCircuitBreakerStorage(storage, cb)
}

// initializeFallback creates and configures fallback strategy based on config
func initializeFallback(config *Config, cache Cache, metrics Metrics, logger Logger) FallbackStrategy {
	if config.FallbackConfig == nil || !config.FallbackConfig.Enabled {
		return nil
	}

	fbConfig := config.FallbackConfig
	var strategies []FallbackStrategy

	// Add cache fallback if enabled
	if fbConfig.FallbackToCache {
		maxStaleness := fbConfig.MaxStaleness
		if maxStaleness == 0 {
			maxStaleness = 5 * time.Minute // default
		}
		strategies = append(strategies, NewCacheFallbackStrategy(cache, maxStaleness, metrics, logger))
	}

	// Add secondary storage fallback if configured
	if fbConfig.SecondaryStorage != nil {
		strategies = append(strategies, NewSecondaryStorageFallbackStrategy(fbConfig.SecondaryStorage, metrics, logger))
	}

	// Add optimistic fallback if enabled
	if fbConfig.OptimisticAllowance {
		percentage := fbConfig.OptimisticAllowancePercentage
		if percentage == 0 {
			percentage = 10.0 // default 10%
		}
		strategies = append(strategies, NewOptimisticFallbackStrategy(percentage, metrics, logger))
	}

	// Return composite strategy if multiple strategies, single strategy if one, or nil if none
	if len(strategies) == 0 {
		return nil
	}
	if len(strategies) == 1 {
		return strategies[0]
	}
	return NewCompositeFallbackStrategy(strategies, metrics, logger)
}

// GetCurrentCycle returns the current billing cycle for a user based on their subscription start date
func (m *Manager) GetCurrentCycle(ctx context.Context, userID string) (Period, error) {
	ent, err := m.storage.GetEntitlement(ctx, userID)
	if err != nil {
		if err == ErrEntitlementNotFound {
			// Use current time as start for users without entitlement
			now := m.now(ctx)
			start, end := CurrentCycleForStart(startOfDayUTC(now), now)
			return Period{
				Start: start,
				End:   end,
				Type:  PeriodTypeMonthly,
			}, nil
		}
		return Period{}, err
	}

	now := m.now(ctx)
	start, end := CurrentCycleForStart(ent.SubscriptionStartDate, now)
	return Period{
		Start: start,
		End:   end,
		Type:  PeriodTypeMonthly,
	}, nil
}

// GetQuota returns current usage and limit for a resource
//
//nolint:gocyclo // Complex function handles multiple period types and error cases
func (m *Manager) GetQuota(ctx context.Context, userID, resource string, periodType PeriodType) (*Usage, error) {
	start := time.Now()
	defer func() {
		m.metrics.RecordQuotaCheck(userID, resource, time.Since(start))
	}()

	// Get entitlement to determine tier (uses cache)
	ent, err := m.GetEntitlement(ctx, userID)
	tier := m.config.DefaultTier
	var period Period

	if err == nil {
		tier = ent.Tier
	}

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Calculate period based on type
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, now)
		} else {
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	case PeriodTypeForever:
		// Forever periods use a stable start time and sentinel end time
		// The period key will be "forever" regardless of dates
		start := startOfDayUTC(now)
		// Use sentinel value for end (or NULL in storage)
		end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		period = Period{Start: start, End: end, Type: PeriodTypeForever}

	default:
		return nil, ErrInvalidPeriod
	}

	// Build cache key for usage
	usageKey := userID + ":" + resource + ":" + period.Key()

	// Check cache first
	if cached, found := m.cache.GetUsage(usageKey); found {
		m.metrics.RecordCacheHit("usage")
		// Ensure limit is set (may be missing in old data)
		if cached.Limit <= 0 {
			cached.Limit = m.getLimitForResource(resource, tier, periodType)
		}
		return cached, nil
	}

	m.metrics.RecordCacheMiss("usage")

	// Use singleflight to prevent cache stampede - deduplicate concurrent requests for same usage key
	result, err, _ := m.usageGroup.Do(usageKey, func() (interface{}, error) {
		// Double-check cache after acquiring the lock (another goroutine might have populated it)
		if cached, found := m.cache.GetUsage(usageKey); found {
			m.metrics.RecordCacheHit("usage")
			return cached, nil
		}

		// Get usage from storage
		uStart := time.Now()
		usage, err := m.storage.GetUsage(ctx, userID, resource, period)
		m.metrics.RecordStorageOperation("GetUsage", time.Since(uStart), err)

		if err != nil {
			m.logger.Error("failed to get usage from storage",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"error", err},
			)

			// Try fallback if available and error warrants it
			if m.fallbackStrategy != nil && m.fallbackStrategy.ShouldFallback(err) {
				m.metrics.RecordFallbackUsage("storage_error")
				fallbackUsage, fallbackErr := m.fallbackStrategy.GetFallbackUsage(ctx, userID, resource, period)
				if fallbackErr == nil && fallbackUsage != nil {
					// Ensure limit is set
					if fallbackUsage.Limit <= 0 {
						fallbackUsage.Limit = m.getLimitForResource(resource, tier, periodType)
					}
					return fallbackUsage, nil
				}
			}

			return nil, err
		}

		// Cache the result if available
		if usage != nil {
			ttl := m.config.CacheTTL
			if m.config.CacheConfig != nil && m.config.CacheConfig.UsageTTL > 0 {
				ttl = m.config.CacheConfig.UsageTTL
			}
			m.cache.SetUsage(usageKey, usage, ttl)
		}

		return usage, nil
	})

	if err != nil {
		return nil, err
	}

	usage, ok := result.(*Usage)
	if !ok {
		return nil, fmt.Errorf("unexpected type from usage fetch: %T", result)
	}

	// If no usage yet, return zero usage with calculated limit
	if usage == nil {
		limit := m.getLimitForResource(resource, tier, periodType)
		// For forever periods, check InitialForeverCredits if limit is 0
		if periodType == PeriodTypeForever && limit == 0 {
			tierConfig, ok := m.config.Tiers[tier]
			if !ok {
				tierConfig, ok = m.config.Tiers[m.config.DefaultTier]
			}
			if ok && tierConfig.InitialForeverCredits != nil {
				if initialLimit, ok := tierConfig.InitialForeverCredits[resource]; ok {
					limit = initialLimit
				}
			}
		}
		return &Usage{
			UserID:   userID,
			Resource: resource,
			Used:     0,
			Limit:    limit,
			Period:   period,
			Tier:     tier,
		}, nil
	}

	// Ensure limit is set (may be missing in old data)
	if usage.Limit <= 0 {
		usage.Limit = m.getLimitForResource(resource, tier, periodType)
	}

	// Record forever credits balance when getting forever quota
	if periodType == PeriodTypeForever && usage.Limit > 0 {
		balance := max(usage.Limit-usage.Used, 0)
		m.metrics.RecordForeverCreditsBalance(resource, tier, balance)
	}

	return usage, nil
}

// Consume consumes quota for a resource
// Returns the new total used amount and any error
//
//nolint:gocyclo // Complex function handles idempotency, period calculation, and error cases
func (m *Manager) Consume(ctx context.Context, userID, resource string, amount int,
	periodType PeriodType, opts ...ConsumeOption) (int, error) {
	// Check if context is already canceled or timed out
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		// Context is still valid, continue
	}

	if amount < 0 {
		return 0, ErrInvalidAmount
	}
	if amount == 0 {
		return 0, nil // No-op
	}

	// Parse options
	consumeOpts := &ConsumeOptions{}
	for _, opt := range opts {
		opt(consumeOpts)
	}

	// Check for duplicate consumption using idempotency key
	if consumeOpts.IdempotencyKey != "" {
		existing, err := m.storage.GetConsumptionRecord(ctx, consumeOpts.IdempotencyKey)
		if err != nil {
			m.logger.Error("failed to check consumption idempotency",
				Field{"userId", userID},
				Field{"idempotencyKey", consumeOpts.IdempotencyKey},
				Field{"error", err},
			)
			return 0, err
		}
		if existing != nil {
			// Duplicate consumption request - return cached result (idempotent)
			m.logger.Info("duplicate consumption request ignored",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"idempotencyKey", consumeOpts.IdempotencyKey},
			)
			m.metrics.RecordIdempotencyHit("consume")
			return existing.NewUsed, nil
		}
	}

	// Get entitlement to determine tier (uses cache)
	ent, err := m.GetEntitlement(ctx, userID)
	tier := m.config.DefaultTier

	// If GetEntitlement fails with a storage/circuit breaker error, return it immediately
	// Only use default tier if entitlement is not found (ErrEntitlementNotFound)
	if err != nil && err != ErrEntitlementNotFound {
		return 0, err
	}

	if err == nil {
		tier = ent.Tier
	}

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Handle cascading consumption for PeriodTypeAuto
	if periodType == PeriodTypeAuto {
		// Get consumption order from tier config
		tierConfig, ok := m.config.Tiers[tier]
		if !ok {
			tierConfig, ok = m.config.Tiers[m.config.DefaultTier]
		}

		consumptionOrder := []PeriodType{PeriodTypeMonthly, PeriodTypeDaily}
		if ok && len(tierConfig.ConsumptionOrder) > 0 {
			consumptionOrder = tierConfig.ConsumptionOrder
		}

		// Try each period in order until one succeeds
		var lastErr error
		for _, pt := range consumptionOrder {
			newUsed, err := m.Consume(ctx, userID, resource, amount, pt, opts...)
			if err == nil {
				return newUsed, nil
			}
			if err != ErrQuotaExceeded {
				// Non-quota error (storage error, etc.) - return immediately
				return 0, err
			}
			// Quota exceeded - try next period
			lastErr = err
		}

		// All periods exhausted
		return 0, lastErr
	}

	// Calculate period for explicit period type
	var period Period
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, now)
		} else {
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	case PeriodTypeForever:
		// Forever periods use a stable start time and sentinel end time
		start := startOfDayUTC(now)
		// Use sentinel value for end (or NULL in storage)
		end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		period = Period{Start: start, End: end, Type: PeriodTypeForever}

	default:
		return 0, ErrInvalidPeriod
	}

	// Check rate limit before quota consumption
	allowed, info, err := m.checkRateLimit(ctx, userID, resource, tier)
	if err != nil {
		// If storage error, allow request (graceful degradation)
		// Log error but don't block quota consumption
		m.logger.Warn("rate limit check failed, allowing request",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"error", err},
		)
	} else if !allowed {
		// Rate limit exceeded
		retryAfter := time.Until(info.ResetTime)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return 0, &RateLimitExceededError{
			Info:       info,
			RetryAfter: retryAfter,
		}
	}

	// Get limit for tier
	limit := m.getLimitForResource(resource, tier, periodType)

	// For forever periods, get actual limit from storage (dynamic credits)
	if periodType == PeriodTypeForever {
		usage, err := m.storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			return 0, fmt.Errorf("failed to get usage for forever period: %w", err)
		}
		if usage != nil && usage.Limit > 0 {
			limit = usage.Limit
			// Record forever credits balance
			balance := usage.Limit - usage.Used
			if balance < 0 {
				balance = 0
			}
			m.metrics.RecordForeverCreditsBalance(resource, tier, balance)
		} else {
			// No forever credits yet
			return 0, ErrQuotaExceeded
		}
	}

	// Check for unlimited quota (-1) - skip limit check
	if limit == -1 {
		// Unlimited quota - proceed without limit validation
		// Storage layer will still track usage but won't enforce limits
	} else if limit <= 0 {
		return 0, ErrQuotaExceeded // No quota available for this tier
	}

	// Check if this is a dry-run (shadow mode)
	if consumeOpts.DryRun {
		// Get current usage to check if it would exceed
		usage, err := m.storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			m.logger.Warn("dry-run: failed to get usage, allowing request",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"error", err},
			)
			// In dry-run mode, allow the request even if we can't check
			return 0, nil
		}

		currentUsed := 0
		if usage != nil {
			currentUsed = usage.Used
		}

		// Check if consumption would exceed limit (skip check for unlimited quota)
		if limit != -1 && currentUsed+amount > limit {
			// Log violation but don't block
			m.logger.Info("dry-run: quota would be exceeded (allowing)",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"currentUsed", currentUsed},
				Field{"amount", amount},
				Field{"limit", limit},
			)
			// Record metric for shadow mode violations
			m.metrics.RecordConsumption(userID, resource, tier, amount, false)
			// Return success in dry-run mode
			return currentUsed + amount, nil
		}

		// Would succeed - log and allow
		m.logger.Info("dry-run: quota check passed",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"currentUsed", currentUsed},
			Field{"amount", amount},
			Field{"limit", limit},
		)
		m.metrics.RecordConsumption(userID, resource, tier, amount, true)
		return currentUsed + amount, nil
	}

	// Consume via storage (transaction-safe)
	cStart := time.Now()
	newUsed, err := m.storage.ConsumeQuota(ctx, &ConsumeRequest{
		UserID:            userID,
		Resource:          resource,
		Amount:            amount,
		Tier:              tier,
		Period:            period,
		Limit:             limit,
		IdempotencyKey:    consumeOpts.IdempotencyKey,
		IdempotencyKeyTTL: m.config.IdempotencyKeyTTL,
	})
	m.metrics.RecordStorageOperation("ConsumeQuota", time.Since(cStart), err)

	// Handle storage failures with fallback
	if err != nil && err != ErrQuotaExceeded {
		// Check if we should use fallback
		if m.fallbackStrategy != nil && m.fallbackStrategy.ShouldFallback(err) {
			m.metrics.RecordFallbackUsage("storage_error")

			// Try to get current usage from fallback
			fallbackUsage, fallbackErr := m.fallbackStrategy.GetFallbackUsage(ctx, userID, resource, period)
			if fallbackErr == nil && fallbackUsage != nil {
				// Check if optimistic consumption is allowed
				if m.fallbackStrategy.AllowOptimisticConsumption(fallbackUsage, amount) {
					// Calculate new used amount optimistically
					optimisticNewUsed := fallbackUsage.Used + amount
					m.logger.Info("allowing optimistic consumption",
						Field{"userId", userID},
						Field{"resource", resource},
						Field{"amount", amount},
						Field{"currentUsed", fallbackUsage.Used},
						Field{"newUsed", optimisticNewUsed},
					)

					// Invalidate cache since we're using optimistic data
					usageKey := userID + ":" + resource + ":" + period.Key()
					m.cache.InvalidateUsage(usageKey)
					m.metrics.RecordConsumption(userID, resource, tier, amount, true)
					if periodType == PeriodTypeForever {
						m.metrics.RecordForeverCreditsConsumption(resource, tier, true)
						m.metrics.RecordForeverCreditsConsumptionAmount(resource, tier, amount)
					}

					// Check for warnings
					m.checkWarnings(ctx, userID, resource, tier, limit, optimisticNewUsed, amount, period)

					return optimisticNewUsed, nil
				}
			}
		}

		// Fallback unavailable or optimistic consumption not allowed - return original error
		m.metrics.RecordConsumption(userID, resource, tier, amount, false)
		if periodType == PeriodTypeForever {
			m.metrics.RecordForeverCreditsConsumption(resource, tier, false)
		}
		m.logger.Error("failed to consume quota",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"error", err},
		)
		return 0, err
	}

	// Invalidate usage cache on successful consumption
	if err == nil {
		usageKey := userID + ":" + resource + ":" + period.Key()
		m.cache.InvalidateUsage(usageKey)
		m.metrics.RecordConsumption(userID, resource, tier, amount, true)

		// Record forever credits specific metrics
		if periodType == PeriodTypeForever {
			m.metrics.RecordForeverCreditsConsumption(resource, tier, true)
			m.metrics.RecordForeverCreditsConsumptionAmount(resource, tier, amount)
			// Check for hybrid billing (user has both monthly and forever)
			monthlyUsage, err := m.storage.GetUsage(ctx, userID, resource, Period{
				Start: period.Start,
				End:   period.End,
				Type:  PeriodTypeMonthly,
			})
			if err == nil && monthlyUsage != nil && (monthlyUsage.Limit > 0 || monthlyUsage.Used > 0) {
				m.metrics.RecordHybridBillingUser(userID)
			}
		}

		// Check for warnings
		m.checkWarnings(ctx, userID, resource, tier, limit, newUsed, amount, period)
	} else {
		m.metrics.RecordConsumption(userID, resource, tier, amount, false)
		if periodType == PeriodTypeForever {
			m.metrics.RecordForeverCreditsConsumption(resource, tier, false)
		}
		if err == ErrQuotaExceeded {
			m.logger.Warn("quota exceeded for user",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"tier", tier},
			)
			// Record quota exhaustion
			m.metrics.RecordQuotaExhaustion(resource, tier, periodType)
		}
	}

	return newUsed, err
}

// tryConsumeZeroAmount handles the zero amount case for TryConsume
func (m *Manager) tryConsumeZeroAmount(ctx context.Context, userID, resource string,
	periodType PeriodType) (*TryConsumeResult, error) {
	usage, err := m.GetQuota(ctx, userID, resource, periodType)
	if err != nil {
		return nil, err
	}
	remaining := calculateRemaining(usage.Limit, usage.Used)
	return &TryConsumeResult{
		Success:   true,
		Remaining: remaining,
		Consumed:  0,
		NewUsed:   usage.Used,
	}, nil
}

// tryConsumeFailureResult creates a failure result for quota exceeded scenarios
func (m *Manager) tryConsumeFailureResult(ctx context.Context, userID, resource string,
	periodType PeriodType, limit int) (*TryConsumeResult, error) {
	usage, err := m.GetQuota(ctx, userID, resource, periodType)
	if err != nil {
		return nil, err
	}
	currentUsed := usage.Used
	remaining := calculateRemaining(limit, currentUsed)
	return &TryConsumeResult{
		Success:   false,
		Remaining: remaining,
		Consumed:  0,
		NewUsed:   currentUsed,
	}, nil
}

// calculateRemaining calculates remaining quota, ensuring it's non-negative
// Returns -1 for unlimited quota
func calculateRemaining(limit, used int) int {
	if limit == -1 {
		return -1 // Unlimited
	}
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ConsumeWithResult consumes quota and returns a ConsumeResult with full quota information.
// This allows applications to trigger side-effects (emails/webhooks) without additional storage calls.
// The method has the same behavior as Consume but returns more detailed information.
func (m *Manager) ConsumeWithResult(ctx context.Context, userID, resource string, amount int,
	periodType PeriodType, opts ...ConsumeOption) (*ConsumeResult, error) {
	newUsed, err := m.Consume(ctx, userID, resource, amount, periodType, opts...)
	if err != nil {
		return nil, err
	}

	// Get usage to get the limit
	usage, err := m.GetQuota(ctx, userID, resource, periodType)
	if err != nil {
		// If we can't get usage, we still return what we know
		// This shouldn't happen after a successful consume, but handle gracefully
		return &ConsumeResult{
			NewUsed:    newUsed,
			Limit:      0,
			Remaining:  0,
			Percentage: 0,
		}, nil
	}

	limit := usage.Limit
	remaining := limit - newUsed
	if remaining < 0 {
		remaining = 0
	}

	var percentage float64
	if limit > 0 {
		percentage = float64(newUsed) / float64(limit) * 100
	} else if limit == -1 {
		// Unlimited quota
		percentage = 0
		remaining = -1
	}

	return &ConsumeResult{
		NewUsed:    newUsed,
		Limit:      limit,
		Remaining:  remaining,
		Percentage: percentage,
	}, nil
}

// GetUsageAfterConsume is a convenience method that calls Consume and then GetQuota.
// This reduces the need for applications to make two separate calls.
// Note: This makes 2 storage calls, so ConsumeWithResult is more efficient.
func (m *Manager) GetUsageAfterConsume(ctx context.Context, userID, resource string, amount int,
	periodType PeriodType, opts ...ConsumeOption) (*Usage, error) {
	_, err := m.Consume(ctx, userID, resource, amount, periodType, opts...)
	if err != nil {
		return nil, err
	}
	return m.GetQuota(ctx, userID, resource, periodType)
}

// TryConsume attempts to consume quota without throwing errors for quota exceeded scenarios.
// It returns a TryConsumeResult indicating success/failure and remaining quota information.
//
// Unlike Consume(), TryConsume() does not return ErrQuotaExceeded. Instead, it returns
// Success: false with the current remaining quota when quota is exceeded. This makes it
// ideal for performance-critical paths where error handling overhead should be minimized.
//
// Error handling:
//   - Quota exceeded: Returns (*TryConsumeResult{Success: false, ...}, nil)
//   - Invalid parameters: Returns (nil, ErrInvalidAmount) or (nil, ErrInvalidPeriod)
//   - Storage failures: Returns (nil, error) - these errors are still propagated
//
// Example usage:
//
//	result, err := manager.TryConsume(ctx, "user1", "api_calls", 10, goquota.PeriodTypeDaily)
//	if err != nil {
//	    // Handle storage/configuration errors
//	    return err
//	}
//	if !result.Success {
//	    // Quota exceeded - use result.Remaining to inform user
//	    return fmt.Errorf("quota exceeded, %d remaining", result.Remaining)
//	}
//	// Success - result.Consumed shows amount consumed, result.Remaining shows remaining
func (m *Manager) TryConsume(ctx context.Context, userID, resource string, amount int,
	periodType PeriodType, opts ...ConsumeOption) (*TryConsumeResult, error) {
	// Validate amount
	if amount < 0 {
		return nil, ErrInvalidAmount
	}
	if amount == 0 {
		return m.tryConsumeZeroAmount(ctx, userID, resource, periodType)
	}

	// Validate period type
	if periodType != PeriodTypeMonthly && periodType != PeriodTypeDaily {
		return nil, ErrInvalidPeriod
	}

	// Get entitlement to determine tier
	ent, err := m.GetEntitlement(ctx, userID)
	tier := m.config.DefaultTier
	if err == nil {
		tier = ent.Tier
	}

	// Get limit for tier
	limit := m.getLimitForResource(resource, tier, periodType)
	// Allow unlimited quota (-1) to proceed
	if limit != -1 && limit <= 0 {
		return m.tryConsumeFailureResult(ctx, userID, resource, periodType, limit)
	}

	// Try to consume via Consume()
	newUsed, err := m.Consume(ctx, userID, resource, amount, periodType, opts...)

	// Handle quota exceeded - convert to TryConsumeResult with Success: false
	if err == ErrQuotaExceeded {
		return m.tryConsumeFailureResult(ctx, userID, resource, periodType, limit)
	}

	// Handle other errors - propagate them
	if err != nil {
		return nil, err
	}

	// Success - calculate remaining quota
	remaining := calculateRemaining(limit, newUsed)
	return &TryConsumeResult{
		Success:   true,
		Remaining: remaining,
		Consumed:  amount,
		NewUsed:   newUsed,
	}, nil
}

// ApplyTierChange applies a tier change with prorated quota adjustment
func (m *Manager) ApplyTierChange(ctx context.Context, userID, oldTier, newTier, resource string) error {
	// Get current cycle
	period, err := m.GetCurrentCycle(ctx, userID)
	if err != nil {
		return err
	}

	// Get current usage
	usage, err := m.storage.GetUsage(ctx, userID, resource, period)
	currentUsed := 0
	if err == nil && usage != nil {
		currentUsed = usage.Used
	}

	// Calculate old and new limits
	oldLimit := m.getLimitForResource(resource, oldTier, PeriodTypeMonthly)
	newLimit := m.getLimitForResource(resource, newTier, PeriodTypeMonthly)

	// Calculate prorated new limit
	now := time.Now().UTC()
	cycleLen := period.End.Sub(period.Start)
	remaining := period.End.Sub(now)

	if cycleLen <= 0 {
		cycleLen = 30 * 24 * time.Hour
	}
	if remaining < 0 {
		remaining = 0
	}

	remainingFrac := float64(remaining) / float64(cycleLen)
	if remainingFrac < 0 {
		remainingFrac = 0
	}
	if remainingFrac > 1 {
		remainingFrac = 1
	}

	// Prorated new limit: used + (newLimit * remainingFrac)
	// Note: math.Round is used to convert fractional results to integers.
	// This can result in 1-unit discrepancies due to rounding:
	//   - Example: 1000 * 0.333 = 333.3 -> rounds to 333 (loses 0.3)
	//   - Example: 1000 * 0.5 = 500.0 -> rounds to 500 (exact)
	//   - Example: 1000 * 0.501 = 501.0 -> rounds to 501 (gains 0.1)
	// These small discrepancies are acceptable for proration calculations and ensure
	// the result is always an integer (as required by the quota limit type).
	proratedNew := int(math.Round(float64(newLimit) * remainingFrac))
	adjustedLimit := currentUsed + proratedNew
	if adjustedLimit < currentUsed {
		adjustedLimit = currentUsed
	}

	m.logger.Info("applying tier change",
		Field{"userId", userID},
		Field{"oldTier", oldTier},
		Field{"newTier", newTier},
		Field{"resource", resource},
	)

	tcStart := time.Now()
	err = m.storage.ApplyTierChange(ctx, &TierChangeRequest{
		UserID:      userID,
		Resource:    resource,
		OldTier:     oldTier,
		NewTier:     newTier,
		Period:      period,
		OldLimit:    oldLimit,
		NewLimit:    adjustedLimit,
		CurrentUsed: currentUsed,
	})
	m.metrics.RecordStorageOperation("ApplyTierChange", time.Since(tcStart), err)

	if err != nil {
		m.logger.Error("failed to apply tier change",
			Field{"userId", userID},
			Field{"oldTier", oldTier},
			Field{"newTier", newTier},
			Field{"error", err},
		)
	}

	return err
}

// SetEntitlement updates a user's entitlement
func (m *Manager) SetEntitlement(ctx context.Context, ent *Entitlement) error {
	start := time.Now()
	err := m.storage.SetEntitlement(ctx, ent)
	m.metrics.RecordStorageOperation("SetEntitlement", time.Since(start), err)

	if err == nil {
		// Invalidate cache on successful update
		m.cache.InvalidateEntitlement(ent.UserID)
		m.logger.Info("updated entitlement for user",
			Field{"userId", ent.UserID},
			Field{"tier", ent.Tier},
		)

		// Apply InitialForeverCredits if configured for this tier
		// Use deterministic idempotency key to prevent race conditions
		tierConfig, ok := m.config.Tiers[ent.Tier]
		if ok && tierConfig.InitialForeverCredits != nil {
			for resource, amount := range tierConfig.InitialForeverCredits {
				if amount > 0 {
					// Use deterministic idempotency key: "initial_bonus_{userID}"
					// This ensures bonus is applied exactly once, even with concurrent requests
					idempotencyKey := fmt.Sprintf("initial_bonus_%s", ent.UserID)
					topUpErr := m.TopUpLimit(ctx, ent.UserID, resource, amount, WithTopUpIdempotencyKey(idempotencyKey))
					if topUpErr != nil && topUpErr != ErrIdempotencyKeyExists {
						// Log error but don't fail entitlement update
						m.logger.Warn("failed to apply initial forever credits",
							Field{"userId", ent.UserID},
							Field{"resource", resource},
							Field{"amount", amount},
							Field{"error", topUpErr},
						)
					}
				}
			}
		}
	} else {
		m.logger.Error("failed to set entitlement for user",
			Field{"userId", ent.UserID},
			Field{"error", err},
		)
	}
	return err
}

// GetEntitlement retrieves a user's entitlement
func (m *Manager) GetEntitlement(ctx context.Context, userID string) (*Entitlement, error) {
	// Check cache first
	if cached, found := m.cache.GetEntitlement(userID); found {
		m.metrics.RecordCacheHit("entitlement")
		return cached, nil
	}

	m.metrics.RecordCacheMiss("entitlement")

	// Use singleflight to prevent cache stampede - deduplicate concurrent requests for same userID
	result, err, _ := m.entitlementGroup.Do(userID, func() (interface{}, error) {
		// Double-check cache after acquiring the lock (another goroutine might have populated it)
		if cached, found := m.cache.GetEntitlement(userID); found {
			m.metrics.RecordCacheHit("entitlement")
			return cached, nil
		}

		// Cache miss - fetch from storage
		start := time.Now()
		ent, err := m.storage.GetEntitlement(ctx, userID)
		m.metrics.RecordStorageOperation("GetEntitlement", time.Since(start), err)

		if err == nil && ent != nil {
			// Cache the result
			ttl := m.config.CacheTTL
			if m.config.CacheConfig != nil && m.config.CacheConfig.EntitlementTTL > 0 {
				ttl = m.config.CacheConfig.EntitlementTTL
			}
			m.cache.SetEntitlement(userID, ent, ttl)
		} else if err != nil && err != ErrEntitlementNotFound {
			// Try fallback on storage errors
			if fallbackEnt := m.tryFallbackEntitlement(ctx, userID, err); fallbackEnt != nil {
				return fallbackEnt, nil
			}
			m.logger.Error("failed to get entitlement from storage",
				Field{"userId", userID},
				Field{"error", err},
			)
		}

		return ent, err
	})

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	ent, ok := result.(*Entitlement)
	if !ok {
		return nil, fmt.Errorf("unexpected type from entitlement fetch: %T", result)
	}
	return ent, nil
}

// tryFallbackEntitlement attempts to get entitlement from fallback strategies
func (m *Manager) tryFallbackEntitlement(ctx context.Context, userID string, err error) *Entitlement {
	if m.fallbackStrategy == nil || !m.fallbackStrategy.ShouldFallback(err) {
		return nil
	}

	m.metrics.RecordFallbackUsage("storage_error")
	fallbackEnt, fallbackErr := m.fallbackStrategy.GetFallbackEntitlement(ctx, userID)
	if fallbackErr == nil && fallbackEnt != nil {
		return fallbackEnt
	}
	return nil
}

// Refund returns consumed quota back to the user
// This is useful for handling failed operations or cancellations
func (m *Manager) Refund(ctx context.Context, req *RefundRequest) error {
	if req.Amount < 0 {
		return ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	// Check for duplicate refund using idempotency key
	if req.IdempotencyKey != "" {
		existing, err := m.storage.GetRefundRecord(ctx, req.IdempotencyKey)
		if err != nil {
			m.logger.Error("failed to check refund idempotency",
				Field{"userId", req.UserID},
				Field{"idempotencyKey", req.IdempotencyKey},
				Field{"error", err},
			)
			return err
		}
		if existing != nil {
			// Duplicate refund request - return success (idempotent)
			m.logger.Info("duplicate refund request ignored",
				Field{"userId", req.UserID},
				Field{"resource", req.Resource},
				Field{"idempotencyKey", req.IdempotencyKey},
			)
			m.metrics.RecordIdempotencyHit("refund")
			return nil
		}
	}

	// Get entitlement to determine period
	ent, err := m.GetEntitlement(ctx, req.UserID)
	var period Period

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Calculate period
	switch req.PeriodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, now)
		} else {
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	case PeriodTypeForever:
		// Forever periods use a stable start time and sentinel end time
		start := startOfDayUTC(now)
		// Use sentinel value for end (or NULL in storage)
		end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		period = Period{Start: start, End: end, Type: PeriodTypeForever}

	default:
		return ErrInvalidPeriod
	}

	// Set period in request so storage uses the correct cycle
	req.Period = period
	// Set TTL for idempotency key
	req.IdempotencyKeyTTL = m.config.IdempotencyKeyTTL

	// Execute refund via storage
	rStart := time.Now()
	err = m.storage.RefundQuota(ctx, req)
	m.metrics.RecordStorageOperation("RefundQuota", time.Since(rStart), err)

	if err == nil {
		// Invalidate usage cache on successful refund
		usageKey := req.UserID + ":" + req.Resource + ":" + period.Key()
		m.cache.InvalidateUsage(usageKey)

		// Record refund metrics
		reason := req.Reason
		if reason == "" {
			reason = "unknown"
		}
		m.metrics.RecordQuotaRefund(req.Resource, reason)
		m.metrics.RecordQuotaRefundAmount(req.Resource, req.Amount)

		m.logger.Info("quota refunded successfully",
			Field{"userId", req.UserID},
			Field{"resource", req.Resource},
			Field{"amount", req.Amount},
			Field{"reason", req.Reason},
		)
	} else {
		m.logger.Error("failed to refund quota",
			Field{"userId", req.UserID},
			Field{"resource", req.Resource},
			Field{"amount", req.Amount},
			Field{"error", err},
		)
	}

	return err
}

// TopUpLimit atomically increments the limit for a resource with PeriodTypeForever
// Used for credit top-ups. Supports idempotency to prevent duplicate processing.
func (m *Manager) TopUpLimit(ctx context.Context, userID, resource string, amount int, opts ...TopUpOption) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}

	// Parse options
	topUpOpts := &TopUpOptions{}
	for _, opt := range opts {
		opt(topUpOpts)
	}

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Create forever period
	start := startOfDayUTC(now)
	// Use sentinel value for end (or NULL in storage)
	end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	period := Period{Start: start, End: end, Type: PeriodTypeForever}

	// Call storage.AddLimit with idempotency key
	err := m.storage.AddLimit(ctx, userID, resource, amount, period, topUpOpts.IdempotencyKey)
	if err == ErrIdempotencyKeyExists {
		// Idempotent operation - already processed, return success
		m.logger.Info("duplicate top-up request ignored (idempotent)",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"idempotencyKey", topUpOpts.IdempotencyKey},
		)
		return nil
	}
	if err != nil {
		m.logger.Error("failed to top up limit",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"amount", amount},
			Field{"error", err},
		)
		return err
	}

	// Invalidate cache
	usageKey := userID + ":" + resource + ":" + period.Key()
	m.cache.InvalidateUsage(usageKey)

	m.logger.Info("limit topped up successfully",
		Field{"userId", userID},
		Field{"resource", resource},
		Field{"amount", amount},
	)

	return nil
}

// RefundCredits atomically decrements the limit for a resource with PeriodTypeForever
// Used for credit refunds. Supports idempotency to prevent duplicate processing.
func (m *Manager) RefundCredits(
	ctx context.Context, userID, resource string, amount int, reason string, opts ...RefundCreditsOption,
) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}

	// Parse options
	refundOpts := &RefundCreditsOptions{}
	for _, opt := range opts {
		opt(refundOpts)
	}

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Create forever period
	start := startOfDayUTC(now)
	// Use sentinel value for end (or NULL in storage)
	end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	period := Period{Start: start, End: end, Type: PeriodTypeForever}

	// Call storage.SubtractLimit with idempotency key
	err := m.storage.SubtractLimit(ctx, userID, resource, amount, period, refundOpts.IdempotencyKey)
	if err == ErrIdempotencyKeyExists {
		// Idempotent operation - already processed, return success
		m.logger.Info("duplicate refund credits request ignored (idempotent)",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"idempotencyKey", refundOpts.IdempotencyKey},
		)
		return nil
	}
	if err != nil {
		m.logger.Error("failed to refund credits",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"amount", amount},
			Field{"error", err},
		)
		return err
	}

	// Invalidate cache
	usageKey := userID + ":" + resource + ":" + period.Key()
	m.cache.InvalidateUsage(usageKey)

	m.logger.Info("credits refunded successfully",
		Field{"userId", userID},
		Field{"resource", resource},
		Field{"amount", amount},
		Field{"reason", reason},
	)

	return nil
}

// checkRateLimit checks if a request is allowed based on rate limiting configuration
func (m *Manager) checkRateLimit(ctx context.Context, userID, resource, tier string) (bool, *RateLimitInfo, error) {
	// Get tier configuration
	tierConfig, ok := m.config.Tiers[tier]
	if !ok {
		// Fall back to default tier
		tierConfig, ok = m.config.Tiers[m.config.DefaultTier]
		if !ok {
			// No tier config, no rate limiting
			return true, nil, nil
		}
	}

	// Check if rate limiting is configured for this resource
	rateLimitConfig, ok := tierConfig.RateLimits[resource]
	if !ok {
		// No rate limit configured for this resource
		return true, nil, nil
	}

	// Check rate limit
	start := time.Now()
	allowed, info, err := m.rateLimiter.Allow(ctx, userID, resource, rateLimitConfig)
	duration := time.Since(start)

	// Record metrics
	m.metrics.RecordRateLimitCheck(userID, resource, allowed, duration)
	if !allowed {
		m.metrics.RecordRateLimitExceeded(userID, resource)
	}

	return allowed, info, err
}

// getLimitForResource returns the quota limit for a resource based on tier and period type
func (m *Manager) getLimitForResource(resource, tier string, periodType PeriodType) int {
	tierConfig, ok := m.config.Tiers[tier]
	if !ok {
		// Fall back to default tier
		tierConfig, ok = m.config.Tiers[m.config.DefaultTier]
		if !ok {
			return 0
		}
	}

	switch periodType {
	case PeriodTypeMonthly:
		if limit, ok := tierConfig.MonthlyQuotas[resource]; ok {
			return limit
		}
	case PeriodTypeDaily:
		if limit, ok := tierConfig.DailyQuotas[resource]; ok {
			return limit
		}
	case PeriodTypeForever:
		// Forever credits are dynamic (purchased), not from tier config
		// Return 0 - the actual limit comes from storage (user's purchased credits)
		// Exception: Check InitialForeverCredits if user has no forever credits yet
		// This is handled in GetQuota when usage is nil
		return 0
	}

	return 0
}

func (m *Manager) checkWarnings(ctx context.Context, userID, resource, tier string,
	limit, currentUsed, amount int, period Period) {
	thresholds := m.getWarningThresholds(resource, tier)
	if len(thresholds) == 0 {
		return
	}

	previousUsed := currentUsed - amount
	for _, threshold := range thresholds {
		boundary := float64(limit) * threshold
		if float64(previousUsed) < boundary && float64(currentUsed) >= boundary {
			// Threshold crossed
			usage := &Usage{
				UserID:    userID,
				Resource:  resource,
				Used:      currentUsed,
				Limit:     limit,
				Period:    period,
				Tier:      tier,
				UpdatedAt: m.now(ctx),
			}

			// Record warning metric
			m.metrics.RecordQuotaWarning(resource, tier, threshold)

			// Determine threshold range for users approaching limit
			usagePercent := float64(currentUsed) / float64(limit)
			var thresholdRange string
			if usagePercent >= 0.9 {
				thresholdRange = "90-100%"
			} else if usagePercent >= 0.8 {
				thresholdRange = "80-90%"
			} else if usagePercent >= 0.5 {
				thresholdRange = "50-80%"
			}
			if thresholdRange != "" {
				m.metrics.RecordUsersApproachingLimit(resource, tier, thresholdRange)
			}

			// Call global handler
			if m.config.WarningHandler != nil {
				m.config.WarningHandler.OnWarning(ctx, usage, threshold)
			}

			// Call context handler if present
			if ctxHandler, ok := ctx.Value(contextWarningKey{}).(WarningHandler); ok {
				ctxHandler.OnWarning(ctx, usage, threshold)
			}
		}
	}
}

func (m *Manager) getWarningThresholds(resource, tier string) []float64 {
	if t, ok := m.config.Tiers[tier]; ok {
		if thresholds, ok := t.WarningThresholds[resource]; ok {
			return thresholds
		}
	}
	return nil
}

// now returns the current time, using TimeSource if available, otherwise time.Now().
// This ensures consistency in distributed systems by using storage engine time
// when available, preventing clock skew issues.
func (m *Manager) now(ctx context.Context) time.Time {
	if m.timeSource != nil {
		t, err := m.timeSource.Now(ctx)
		if err == nil {
			return t.UTC()
		}
		// Log error but fall back to local time
		m.logger.Warn("failed to get time from storage, using local time",
			Field{"error", err},
		)
	}
	return time.Now().UTC()
}

// logAuditEntry logs an audit entry if the storage implements AuditLogger.
// This is a helper method that safely checks for AuditLogger implementation.
func (m *Manager) logAuditEntry(ctx context.Context, entry *AuditLogEntry) {
	if auditLogger, ok := m.storage.(AuditLogger); ok {
		if err := auditLogger.LogAuditEntry(ctx, entry); err != nil {
			// Log error but don't fail the operation
			m.logger.Warn("failed to log audit entry",
				Field{"action", entry.Action},
				Field{"userId", entry.UserID},
				Field{"error", err},
			)
		}
	}
}

// GetAuditLogs retrieves audit logs if the storage implements AuditLogger.
// Returns an error if storage doesn't implement AuditLogger or if query fails.
func (m *Manager) GetAuditLogs(ctx context.Context, filter AuditLogFilter) ([]*AuditLogEntry, error) {
	auditLogger, ok := m.storage.(AuditLogger)
	if !ok {
		return nil, fmt.Errorf("storage does not implement AuditLogger")
	}
	return auditLogger.GetAuditLogs(ctx, filter)
}

// SetUsage manually sets the used amount for a specific resource and period.
// This is useful for administrative operations like quota resets or corrections.
// The limit is automatically determined from the user's tier configuration.
func (m *Manager) SetUsage(ctx context.Context, userID, resource string, periodType PeriodType, amount int) error {
	if amount < 0 {
		return ErrInvalidAmount
	}

	// Get entitlement to determine tier
	ent, err := m.GetEntitlement(ctx, userID)
	tier := m.config.DefaultTier
	if err == nil && ent != nil {
		tier = ent.Tier
	}

	// Get current time (using TimeSource if available)
	now := m.now(ctx)

	// Calculate period based on type
	var period Period
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil && ent != nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, now)
		} else {
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	case PeriodTypeForever:
		start := startOfDayUTC(now)
		end := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		period = Period{Start: start, End: end, Type: PeriodTypeForever}

	default:
		return ErrInvalidPeriod
	}

	// Get limit for resource
	limit := m.getLimitForResource(resource, tier, periodType)

	// For forever periods, get actual limit from storage (dynamic credits)
	if periodType == PeriodTypeForever {
		usage, err := m.storage.GetUsage(ctx, userID, resource, period)
		if err != nil {
			return fmt.Errorf("failed to get usage for forever period: %w", err)
		}
		if usage != nil && usage.Limit > 0 {
			limit = usage.Limit
		}
	}

	// Create usage object
	usage := &Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      amount,
		Limit:     limit,
		Period:    period,
		Tier:      tier,
		UpdatedAt: m.now(ctx),
	}

	// Set usage via storage
	sStart := time.Now()
	err = m.storage.SetUsage(ctx, userID, resource, usage, period)
	m.metrics.RecordStorageOperation("SetUsage", time.Since(sStart), err)

	if err != nil {
		m.logger.Error("failed to set usage",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"amount", amount},
			Field{"error", err},
		)
		return err
	}

	// Invalidate cache
	usageKey := userID + ":" + resource + ":" + period.Key()
	m.cache.InvalidateUsage(usageKey)

	m.logger.Info("usage set successfully",
		Field{"userId", userID},
		Field{"resource", resource},
		Field{"amount", amount},
		Field{"periodType", periodType},
	)

	// Log audit entry
	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("%s-%s-%d", userID, resource, m.now(ctx).UnixNano()),
		UserID:    userID,
		Resource:  resource,
		Action:    "admin_set",
		Amount:    amount,
		Timestamp: m.now(ctx),
		Actor:     "system", // Could be enhanced to accept actor from context
		Reason:    "administrative_set",
		Metadata: map[string]string{
			"periodType": string(periodType),
			"limit":      fmt.Sprintf("%d", limit),
		},
	})

	return nil
}

// GrantOneTimeCredit grants temporary "overflow" capacity without changing the user's plan.
// This adds credits to the user's forever credits pool, which can be used for any period type
// when using PeriodTypeAuto consumption.
func (m *Manager) GrantOneTimeCredit(ctx context.Context, userID, resource string, amount int) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}

	// Use TopUpLimit for forever credits (one-time bonus)
	err := m.TopUpLimit(ctx, userID, resource, amount)
	if err != nil {
		m.logger.Error("failed to grant one-time credit",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"amount", amount},
			Field{"error", err},
		)
		return err
	}

	m.logger.Info("one-time credit granted successfully",
		Field{"userId", userID},
		Field{"resource", resource},
		Field{"amount", amount},
	)

	// Log audit entry
	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("%s-%s-%d", userID, resource, m.now(ctx).UnixNano()),
		UserID:    userID,
		Resource:  resource,
		Action:    "admin_grant_credit",
		Amount:    amount,
		Timestamp: m.now(ctx),
		Actor:     "system", // Could be enhanced to accept actor from context
		Reason:    "one_time_credit",
		Metadata: map[string]string{
			"periodType": "forever",
		},
	})

	return nil
}

// ResetUsage resets the usage to zero for a specific resource and period.
// This is a convenience method that calls SetUsage with amount 0.
func (m *Manager) ResetUsage(ctx context.Context, userID, resource string, periodType PeriodType) error {
	return m.SetUsage(ctx, userID, resource, periodType, 0)
}
