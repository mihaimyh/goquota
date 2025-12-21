package goquota

import (
	"context"
	"math"
	"time"
)

// Manager manages quota consumption and tracking across multiple resources and time periods
type Manager struct {
	storage Storage
	config  Config
}

// NewManager creates a new quota manager with the given storage and configuration
func NewManager(storage Storage, config Config) (*Manager, error) {
	if storage == nil {
		return nil, ErrStorageUnavailable
	}

	// Set defaults
	if config.CacheTTL == 0 {
		config.CacheTTL = time.Minute
	}
	if config.DefaultTier == "" {
		config.DefaultTier = "explorer"
	}

	return &Manager{
		storage: storage,
		config:  config,
	}, nil
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
	// Get entitlement to determine tier
	ent, err := m.storage.GetEntitlement(ctx, userID)
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
	usage, err := m.storage.GetUsage(ctx, userID, resource, period)
	if err != nil {
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
func (m *Manager) Consume(ctx context.Context, userID, resource string, amount int, periodType PeriodType) error {
	if amount < 0 {
		return ErrInvalidAmount
	}
	if amount == 0 {
		return nil // No-op
	}

	// Get entitlement to determine tier
	ent, err := m.storage.GetEntitlement(ctx, userID)
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
		return ErrInvalidPeriod
	}

	// Get limit for tier
	limit := m.getLimitForResource(resource, tier, periodType)
	if limit <= 0 {
		return ErrQuotaExceeded // No quota available for this tier
	}

	// Consume via storage (transaction-safe)
	return m.storage.ConsumeQuota(ctx, &ConsumeRequest{
		UserID:   userID,
		Resource: resource,
		Amount:   amount,
		Tier:     tier,
		Period:   period,
		Limit:    limit,
	})
}

// ApplyTierChange applies a tier change with prorated quota adjustment
func (m *Manager) ApplyTierChange(ctx context.Context, userID, oldTier, newTier string, resource string) error {
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

	return m.storage.ApplyTierChange(ctx, &TierChangeRequest{
		UserID:      userID,
		OldTier:     oldTier,
		NewTier:     newTier,
		Period:      period,
		OldLimit:    oldLimit,
		NewLimit:    adjustedLimit,
		CurrentUsed: currentUsed,
	})
}

// SetEntitlement updates a user's entitlement
func (m *Manager) SetEntitlement(ctx context.Context, ent *Entitlement) error {
	return m.storage.SetEntitlement(ctx, ent)
}

// GetEntitlement retrieves a user's entitlement
func (m *Manager) GetEntitlement(ctx context.Context, userID string) (*Entitlement, error) {
	return m.storage.GetEntitlement(ctx, userID)
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
