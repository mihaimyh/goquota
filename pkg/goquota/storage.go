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

	// AddLimit atomically increments the limit for a resource/period.
	// Used for credit top-ups to prevent race conditions.
	// If usage record doesn't exist, creates it with limit = amount.
	// idempotencyKey: If provided, ensures operation is idempotent (checks inside transaction).
	// Returns error if operation fails, or ErrIdempotencyKeyExists if already processed.
	AddLimit(ctx context.Context, userID, resource string, amount int, period Period, idempotencyKey string) error

	// SubtractLimit atomically decrements the limit for a resource/period.
	// Used for credit refunds. Prevents negative limits (clamps to 0).
	// idempotencyKey: If provided, ensures operation is idempotent (checks inside transaction).
	// Returns error if operation fails, or ErrIdempotencyKeyExists if already processed.
	SubtractLimit(ctx context.Context, userID, resource string, amount int, period Period, idempotencyKey string) error
}

// TimeSource defines an interface for getting time from the storage engine.
// This ensures consistency in distributed systems by using storage engine time
// instead of application server time, preventing clock skew issues.
type TimeSource interface {
	// Now returns the current time from the storage engine.
	// This should use the storage engine's time (e.g., Redis TIME command)
	// to ensure consistency across distributed systems.
	// Returns an error if the storage engine doesn't support time queries.
	Now(ctx context.Context) (time.Time, error)
}

// AuditLogEntry represents a single audit log entry for quota operations.
type AuditLogEntry struct {
	// ID is a unique identifier for this audit log entry
	ID string

	// UserID is the user who performed or was affected by the action
	UserID string

	// Resource is the resource affected by the action
	Resource string

	// Action is the type of action performed (e.g., "consume", "refund", "tier_change", "admin_set")
	Action string

	// Amount is the amount affected by the action (can be 0 for non-quantitative actions)
	Amount int

	// Timestamp is when the action occurred
	Timestamp time.Time

	// Actor is who performed the action ("system" for automatic operations, user ID for admin actions)
	Actor string

	// Reason is an optional reason for the action (e.g., "failed_operation", "admin_correction")
	Reason string

	// Metadata contains additional context about the action
	Metadata map[string]string
}

// AuditLogFilter defines filters for querying audit logs.
type AuditLogFilter struct {
	// UserID filters by user ID (optional)
	UserID string

	// Resource filters by resource (optional)
	Resource string

	// Action filters by action type (optional)
	Action string

	// StartTime filters entries after this time (optional)
	StartTime *time.Time

	// EndTime filters entries before this time (optional)
	EndTime *time.Time

	// Limit limits the number of results returned (default: 100)
	Limit int
}

// AuditLogger defines the interface for audit logging.
// Storage implementations can optionally implement this interface to provide audit logging.
type AuditLogger interface {
	// LogAuditEntry logs an audit entry.
	// This should be called for all quota-changing operations.
	LogAuditEntry(ctx context.Context, entry *AuditLogEntry) error

	// GetAuditLogs retrieves audit logs matching the filter.
	// Returns a list of audit log entries in chronological order (newest first).
	GetAuditLogs(ctx context.Context, filter AuditLogFilter) ([]*AuditLogEntry, error)
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
