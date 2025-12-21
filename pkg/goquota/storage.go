package goquota

import "context"

// Storage defines the interface for quota persistence
type Storage interface {
// GetEntitlement retrieves user's entitlement
GetEntitlement(ctx context.Context, userID string) (*Entitlement, error)

// SetEntitlement stores user's entitlement
SetEntitlement(ctx context.Context, ent *Entitlement) error

// GetUsage retrieves usage for a specific period
GetUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error)

// ConsumeQuota atomically consumes quota (transaction-safe)
// Returns ErrQuotaExceeded if consumption would exceed limit
ConsumeQuota(ctx context.Context, req ConsumeRequest) error

// ApplyTierChange applies prorated quota adjustment for tier changes
ApplyTierChange(ctx context.Context, req TierChangeRequest) error
}

// ConsumeRequest represents a quota consumption request
type ConsumeRequest struct {
UserID   string
Resource string
Amount   int
Tier     string
Period   Period
Limit    int
}

// TierChangeRequest represents a tier change with proration
type TierChangeRequest struct {
UserID      string
OldTier     string
NewTier     string
Period      Period
OldLimit    int
NewLimit    int
CurrentUsed int
}