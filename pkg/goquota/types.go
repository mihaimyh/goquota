package goquota

import "time"

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
}

// Config holds quota manager configuration
type Config struct {
// Tiers maps tier names to their quota limits
Tiers map[string]TierConfig

// DefaultTier is used when user has no entitlement
DefaultTier string

// CacheTTL is the duration to cache entitlements (default: 1 minute)
CacheTTL time.Duration
}