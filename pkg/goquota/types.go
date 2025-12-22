package goquota

import (
	"context"
	"time"
)

// PeriodType defines the type of quota period
type PeriodType string

const (
	// PeriodTypeDaily represents a daily quota period
	PeriodTypeDaily PeriodType = "daily"
	// PeriodTypeMonthly represents a monthly quota period (anniversary-based)
	PeriodTypeMonthly PeriodType = "monthly"
)

// Period represents a quota period with start and end times
type Period struct {
	Start time.Time
	End   time.Time
	Type  PeriodType
}

// Key returns a stable string key for this period
func (p Period) Key() string {
	switch p.Type {
	case PeriodTypeDaily:
		return p.Start.UTC().Format("2006-01-02")
	case PeriodTypeMonthly:
		return p.Start.UTC().Format("2006-01-02")
	default:
		return p.Start.UTC().Format("2006-01-02")
	}
}

// Entitlement represents a user's subscription entitlement
type Entitlement struct {
	UserID                string
	Tier                  string
	SubscriptionStartDate time.Time
	ExpiresAt             *time.Time
	UpdatedAt             time.Time
}

// Usage represents quota usage for a specific resource and period
type Usage struct {
	UserID    string
	Resource  string
	Used      int
	Limit     int
	Period    Period
	Tier      string
	UpdatedAt time.Time
}

// TierConfig defines quota limits for a specific tier
type TierConfig struct {
	Name string

	// MonthlyQuotas maps resource names to monthly limits
	MonthlyQuotas map[string]int

	// DailyQuotas maps resource names to daily limits
	DailyQuotas map[string]int

	// WarningThresholds maps resource names to a list of usage percentages (e.g., [0.8, 0.9])
	// that should trigger warnings.
	WarningThresholds map[string][]float64
}

// CacheConfig holds cache configuration
type CacheConfig struct {
	// Enabled determines if caching is active
	Enabled bool

	// EntitlementTTL is the TTL for cached entitlements (default: 1 minute)
	EntitlementTTL time.Duration

	// UsageTTL is the TTL for cached usage data (default: 10 seconds)
	UsageTTL time.Duration

	// MaxEntitlements is the maximum number of entitlements to cache (default: 1000)
	MaxEntitlements int

	// MaxUsage is the maximum number of usage records to cache (default: 10000)
	MaxUsage int
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	// Enabled determines if the circuit breaker is active
	Enabled bool

	// FailureThreshold is the number of consecutive failures before opening the circuit (default: 5)
	FailureThreshold int

	// ResetTimeout is the duration to wait before transitioning from Open to Half-Open (default: 30 seconds)
	ResetTimeout time.Duration
}

// Config holds quota manager configuration
type Config struct {
	// Tiers maps tier names to their quota limits
	Tiers map[string]TierConfig

	// DefaultTier is used when user has no entitlement
	DefaultTier string

	// CacheTTL is the duration to cache entitlements (default: 1 minute)
	// Deprecated: Use CacheConfig.EntitlementTTL instead
	CacheTTL time.Duration

	// CacheConfig configures the caching layer
	CacheConfig *CacheConfig

	// Metrics is used for tracking quota operations (default: NoopMetrics)
	Metrics Metrics

	// Logger is used for structured logging (default: NoopLogger)
	Logger Logger

	// WarningHandler is called when a warning threshold is crossed (optional)
	WarningHandler WarningHandler

	// CircuitBreakerConfig configures the circuit breaker
	CircuitBreakerConfig *CircuitBreakerConfig

	// IdempotencyKeyTTL is the TTL for idempotency keys (default: 24 hours)
	IdempotencyKeyTTL time.Duration
}

// WarningHandler is the interface for handling quota warnings
type WarningHandler interface {
	OnWarning(ctx context.Context, usage *Usage, threshold float64)
}

// ConsumeOption represents an option for the Consume operation
type ConsumeOption func(*ConsumeOptions)

// ConsumeOptions holds options for the Consume operation
type ConsumeOptions struct {
	IdempotencyKey string
}

// WithIdempotencyKey sets the idempotency key for a consume operation
func WithIdempotencyKey(key string) ConsumeOption {
	return func(opts *ConsumeOptions) {
		opts.IdempotencyKey = key
	}
}

// RefundRequest represents a quota refund request
type RefundRequest struct {
	UserID         string
	Resource       string
	Amount         int
	PeriodType     PeriodType
	Period         Period // Populated by Manager
	IdempotencyKey string
	Reason         string
	Metadata       map[string]string
}

// RefundRecord represents an audit record for a refund
type RefundRecord struct {
	RefundID       string
	UserID         string
	Resource       string
	Amount         int
	Period         Period
	Timestamp      time.Time
	IdempotencyKey string
	Reason         string
	Metadata       map[string]string
}

// ConsumptionRecord represents an audit record for a quota consumption
type ConsumptionRecord struct {
	ConsumptionID  string
	UserID         string
	Resource       string
	Amount         int
	Period         Period
	Timestamp      time.Time
	IdempotencyKey string
	NewUsed        int
	Metadata       map[string]string
}
