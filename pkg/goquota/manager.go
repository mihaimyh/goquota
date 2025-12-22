package goquota

import (
	"context"
	"math"
	"time"
)

type contextWarningKey struct{}

// WithWarningHandler returns a new context with a warning handler.
// This allows per-request warning handling (e.g. in middleware).
func WithWarningHandler(ctx context.Context, handler WarningHandler) context.Context {
	return context.WithValue(ctx, contextWarningKey{}, handler)
}

// Manager manages quota consumption and tracking across multiple resources and time periods
type Manager struct {
	storage Storage
	config  Config
	cache   Cache
	metrics Metrics
	logger  Logger
}

// NewManager creates a new quota manager with the given storage and configuration
func NewManager(storage Storage, config *Config) (*Manager, error) {
	if storage == nil {
		return nil, ErrStorageUnavailable
	}
	if config == nil {
		config = &Config{}
	}

	applyConfigDefaults(config)
	cache := initializeCache(config)
	metrics := initializeMetrics(config.Metrics)
	logger := initializeLogger(config.Logger)
	currentStorage := initializeCircuitBreaker(storage, config.CircuitBreakerConfig, metrics)

	return &Manager{
		storage: currentStorage,
		config:  *config,
		cache:   cache,
		metrics: metrics,
		logger:  logger,
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

// GetCurrentCycle returns the current billing cycle for a user based on their subscription start date
func (m *Manager) GetCurrentCycle(ctx context.Context, userID string) (Period, error) {
	ent, err := m.storage.GetEntitlement(ctx, userID)
	if err != nil {
		if err == ErrEntitlementNotFound {
			// Use current time as start for users without entitlement
			now := time.Now().UTC()
			start, end := CurrentCycleForStart(startOfDayUTC(now), now)
			return Period{
				Start: start,
				End:   end,
				Type:  PeriodTypeMonthly,
			}, nil
		}
		return Period{}, err
	}

	start, end := CurrentCycleForStart(ent.SubscriptionStartDate, time.Now().UTC())
	return Period{
		Start: start,
		End:   end,
		Type:  PeriodTypeMonthly,
	}, nil
}

// GetQuota returns current usage and limit for a resource
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

	// Calculate period based on type
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, time.Now().UTC())
		} else {
			now := time.Now().UTC()
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		now := time.Now().UTC()
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	default:
		return nil, ErrInvalidPeriod
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
		return nil, err
	}

	// If no usage yet, return zero usage with calculated limit
	if usage == nil {
		limit := m.getLimitForResource(resource, tier, periodType)
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

	return usage, nil
}

// Consume consumes quota for a resource
// Returns the new total used amount and any error
func (m *Manager) Consume(ctx context.Context, userID, resource string, amount int,
	periodType PeriodType, opts ...ConsumeOption) (int, error) {
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
			return existing.NewUsed, nil
		}
	}

	// Get entitlement to determine tier (uses cache)
	ent, err := m.GetEntitlement(ctx, userID)
	tier := m.config.DefaultTier
	var period Period

	if err == nil {
		tier = ent.Tier
	}

	// Calculate period
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, time.Now().UTC())
		} else {
			now := time.Now().UTC()
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		now := time.Now().UTC()
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	default:
		return 0, ErrInvalidPeriod
	}

	// Get limit for tier
	limit := m.getLimitForResource(resource, tier, periodType)
	if limit <= 0 {
		return 0, ErrQuotaExceeded // No quota available for this tier
	}

	// Consume via storage (transaction-safe)
	cStart := time.Now()
	newUsed, err := m.storage.ConsumeQuota(ctx, &ConsumeRequest{
		UserID:         userID,
		Resource:       resource,
		Amount:         amount,
		Tier:           tier,
		Period:         period,
		Limit:          limit,
		IdempotencyKey: consumeOpts.IdempotencyKey,
	})
	m.metrics.RecordStorageOperation("ConsumeQuota", time.Since(cStart), err)

	// Invalidate usage cache on successful consumption
	if err == nil {
		usageKey := userID + ":" + resource + ":" + period.Key()
		m.cache.InvalidateUsage(usageKey)
		m.metrics.RecordConsumption(userID, resource, tier, amount, true)

		// Check for warnings
		m.checkWarnings(ctx, userID, resource, tier, limit, newUsed, amount, period)
	} else {
		m.metrics.RecordConsumption(userID, resource, tier, amount, false)
		if err == ErrQuotaExceeded {
			m.logger.Warn("quota exceeded for user",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"tier", tier},
			)
		} else {
			m.logger.Error("failed to consume quota",
				Field{"userId", userID},
				Field{"resource", resource},
				Field{"error", err},
			)
		}
	}

	return newUsed, err
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
		m.logger.Error("failed to get entitlement from storage",
			Field{"userId", userID},
			Field{"error", err},
		)
	}

	return ent, err
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
			return nil
		}
	}

	// Get entitlement to determine period
	ent, err := m.GetEntitlement(ctx, req.UserID)
	var period Period

	// Calculate period
	switch req.PeriodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		if err == nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, time.Now().UTC())
		} else {
			now := time.Now().UTC()
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		period = Period{Start: start, End: end, Type: PeriodTypeMonthly}

	case PeriodTypeDaily:
		now := time.Now().UTC()
		start := startOfDayUTC(now)
		end := start.Add(24 * time.Hour)
		period = Period{Start: start, End: end, Type: PeriodTypeDaily}

	default:
		return ErrInvalidPeriod
	}

	// Set period in request so storage uses the correct cycle
	req.Period = period

	// Execute refund via storage
	rStart := time.Now()
	err = m.storage.RefundQuota(ctx, req)
	m.metrics.RecordStorageOperation("RefundQuota", time.Since(rStart), err)

	if err == nil {
		// Invalidate usage cache on successful refund
		usageKey := req.UserID + ":" + req.Resource + ":" + period.Key()
		m.cache.InvalidateUsage(usageKey)

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
				UpdatedAt: time.Now().UTC(),
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
