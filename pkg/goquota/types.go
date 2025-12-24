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
	// PeriodTypeForever represents a non-expiring quota period (pre-paid credits)
	PeriodTypeForever PeriodType = "forever"
	// PeriodTypeAuto triggers cascading consumption (uses ConsumptionOrder from TierConfig)
	PeriodTypeAuto PeriodType = "auto"
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
	case PeriodTypeForever:
		return "forever" // Stable key for forever periods (no date component)
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

	// RateLimits maps resource names to rate limit configurations
	// Rate limits enforce time-based request frequency (e.g., 10 requests/second)
	// while quotas enforce total usage limits (e.g., 1000 requests/month)
	RateLimits map[string]RateLimitConfig

	// ConsumptionOrder defines the priority order for quota consumption when PeriodTypeAuto is used.
	// Example: []PeriodType{PeriodTypeMonthly, PeriodTypeForever}
	// Consumes from Monthly first, then falls back to Forever credits.
	// If empty, defaults to [PeriodTypeMonthly, PeriodTypeDaily] for backward compatibility.
	ConsumptionOrder []PeriodType

	// InitialForeverCredits are sign-up bonuses applied only when user first gets this tier
	// (if no forever credits exist yet). This is NOT a recurring quota - forever credits are
	// dynamic (purchased via top-ups). Only applied once per user using deterministic idempotency key.
	InitialForeverCredits map[string]int
}

// RateLimitConfig defines rate limiting configuration for a resource
type RateLimitConfig struct {
	// Algorithm specifies the rate limiting algorithm to use
	// Options: "token_bucket" (allows burst traffic) or "sliding_window" (precise rate limiting)
	Algorithm string

	// Rate is the number of requests allowed per window
	Rate int

	// Window is the time window for the rate limit (e.g., 1s, 1m, 1h)
	Window time.Duration

	// Burst is the maximum burst capacity for token bucket algorithm
	// For sliding window, this field is ignored
	Burst int
}

// RateLimitInfo contains information about a rate limit check result
type RateLimitInfo struct {
	// Remaining is the number of requests remaining in the current window
	Remaining int

	// ResetTime is when the rate limit window resets
	ResetTime time.Time

	// Limit is the total rate limit for the window
	Limit int
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

// FallbackConfig holds fallback strategy configuration
type FallbackConfig struct {
	// Enabled determines if fallback strategies are active
	Enabled bool

	// FallbackToCache enables falling back to cached data when storage fails
	FallbackToCache bool

	// OptimisticAllowance enables optimistic quota consumption when storage is unavailable
	OptimisticAllowance bool

	// OptimisticAllowancePercentage is the maximum percentage of quota to allow optimistically (default: 10%)
	// For example, if limit is 1000 and percentage is 10%, up to 100 units can be consumed optimistically
	OptimisticAllowancePercentage float64

	// SecondaryStorage is an optional secondary storage backend to use when primary storage fails
	// If nil, secondary storage fallback is disabled
	SecondaryStorage Storage

	// ManualOverrideMode enables manual override mode (for future use)
	ManualOverrideMode bool

	// MaxStaleness is the maximum age of cached data to use for fallback (default: 5 minutes)
	// Cached data older than this will not be used for fallback
	MaxStaleness time.Duration
}

// FallbackStrategy defines the interface for fallback strategies
// Fallback strategies provide degraded mode operation when storage is unavailable
type FallbackStrategy interface {
	// ShouldFallback determines if an error warrants using fallback strategies
	// Returns true if the error indicates storage unavailability (e.g., circuit open, network error)
	// Returns false for business logic errors (e.g., quota exceeded, invalid tier)
	ShouldFallback(err error) bool

	// GetFallbackUsage attempts to retrieve usage data from fallback sources
	// Returns usage data if available from fallback, or error if fallback unavailable
	GetFallbackUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error)

	// GetFallbackEntitlement attempts to retrieve entitlement data from fallback sources
	// Returns entitlement data if available from fallback, or error if fallback unavailable
	GetFallbackEntitlement(ctx context.Context, userID string) (*Entitlement, error)

	// AllowOptimisticConsumption checks if optimistic consumption is allowed for the given usage and amount
	// Returns true if the consumption can be allowed optimistically (within configured limits)
	AllowOptimisticConsumption(usage *Usage, amount int) bool
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

	// FallbackConfig configures fallback strategies for degraded mode operation
	FallbackConfig *FallbackConfig
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
	UserID            string
	Resource          string
	Amount            int
	PeriodType        PeriodType
	Period            Period // Populated by Manager
	IdempotencyKey    string
	IdempotencyKeyTTL time.Duration // TTL for idempotency key expiration
	Reason            string
	Metadata          map[string]string
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

// TryConsumeResult represents the result of a TryConsume operation
type TryConsumeResult struct {
	// Success indicates whether the consumption succeeded
	Success bool

	// Remaining is the remaining quota after consumption (or current remaining if failed)
	Remaining int

	// Consumed is the amount actually consumed (0 if failed)
	Consumed int

	// NewUsed is the new total used amount (same as current if failed)
	NewUsed int
}

// TopUpOption represents an option for the TopUpLimit operation
type TopUpOption func(*TopUpOptions)

// TopUpOptions holds options for the TopUpLimit operation
type TopUpOptions struct {
	IdempotencyKey string
}

// WithTopUpIdempotencyKey sets the idempotency key for a top-up operation
func WithTopUpIdempotencyKey(key string) TopUpOption {
	return func(opts *TopUpOptions) {
		opts.IdempotencyKey = key
	}
}

// RefundCreditsOption represents an option for the RefundCredits operation
type RefundCreditsOption func(*RefundCreditsOptions)

// RefundCreditsOptions holds options for the RefundCredits operation
type RefundCreditsOptions struct {
	IdempotencyKey string
}

// WithRefundIdempotencyKey sets the idempotency key for a refund credits operation
func WithRefundIdempotencyKey(key string) RefundCreditsOption {
	return func(opts *RefundCreditsOptions) {
		opts.IdempotencyKey = key
	}
}
