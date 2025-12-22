package goquota

import (
	"context"
	"time"
)

// Storage defines the interface for quota persistence
// All methods use concrete types from this package to avoid import cycles
type Storage interface {
	// GetEntitlement retrieves user's entitlement
	// Returns *Entitlement or error
	GetEntitlement(ctx context.Context, userID string) (*Entitlement, error)

	// SetEntitlement stores user's entitlement
	SetEntitlement(ctx context.Context, ent *Entitlement) error

	// GetUsage retrieves usage for a specific period
	// Returns *Usage, nil (if no usage), or error
	GetUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error)

	// ConsumeQuota atomically consumes quota (transaction-safe)
	// Returns the new total used amount and any error (e.g. ErrQuotaExceeded)
	ConsumeQuota(ctx context.Context, req *ConsumeRequest) (int, error)

	// ApplyTierChange applies prorated quota adjustment for tier changes
	ApplyTierChange(ctx context.Context, req *TierChangeRequest) error

	// SetUsage manually sets usage for a specific period
	SetUsage(ctx context.Context, userID, resource string, usage *Usage, period Period) error

	// RefundQuota returns consumed quota back to the user
	// Returns error if refund would exceed original limit or if idempotency key is duplicate
	RefundQuota(ctx context.Context, req *RefundRequest) error

	// GetRefundRecord retrieves a refund record by idempotency key
	// Returns nil if no record found (not an error)
	GetRefundRecord(ctx context.Context, idempotencyKey string) (*RefundRecord, error)

	// GetConsumptionRecord retrieves a consumption record by idempotency key
	// Returns nil if no record found (not an error)
	GetConsumptionRecord(ctx context.Context, idempotencyKey string) (*ConsumptionRecord, error)

	// CheckRateLimit checks if a rate limit allows the request
	// Returns (allowed, remaining, resetTime, error)
	// allowed: true if the request is allowed, false if rate limited
	// remaining: number of requests remaining in the current window
	// resetTime: when the rate limit window resets
	CheckRateLimit(ctx context.Context, req *RateLimitRequest) (bool, int, time.Time, error)

	// RecordRateLimitRequest records a rate limit request (for sliding window algorithm)
	// This is called after CheckRateLimit when the request is allowed
	RecordRateLimitRequest(ctx context.Context, req *RateLimitRequest) error
}

// ConsumeRequest represents a quota consumption request
type ConsumeRequest struct {
	UserID            string
	Resource          string
	Amount            int
	Tier              string
	Period            Period
	Limit             int
	IdempotencyKey    string
	IdempotencyKeyTTL time.Duration // TTL for idempotency key expiration
}

// TierChangeRequest represents a tier change with proration
// TierChangeRequest represents a tier change with proration
type TierChangeRequest struct {
	UserID      string
	Resource    string
	OldTier     string
	NewTier     string
	Period      Period
	OldLimit    int
	NewLimit    int
	CurrentUsed int
}

// RateLimitRequest represents a rate limit check request
type RateLimitRequest struct {
	UserID    string
	Resource  string
	Algorithm string // "token_bucket" or "sliding_window"
	Rate      int
	Window    time.Duration
	Burst     int // for token bucket
	Now       time.Time
}
