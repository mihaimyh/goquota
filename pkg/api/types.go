package api

import "time"

// UsageResponse represents the complete quota state for a user
type UsageResponse struct {
	UserID    string                   `json:"user_id"`
	Tier      string                   `json:"tier"`
	Status    string                   `json:"status"` // "active", "expired", "default"
	Resources map[string]ResourceUsage `json:"resources"`
}

// ResourceUsage represents quota information for a single resource
type ResourceUsage struct {
	Limit     int              `json:"limit"`              // Combined limit (-1 for unlimited)
	Used      int              `json:"used"`               // Combined used amount
	Remaining int              `json:"remaining"`          // Combined remaining (-1 for unlimited)
	ResetAt   *time.Time       `json:"reset_at,omitempty"` // Reset time for monthly quota
	Breakdown []QuotaBreakdown `json:"breakdown"`          // Breakdown by source
}

// QuotaBreakdown represents quota information from a specific source
type QuotaBreakdown struct {
	Source  string `json:"source"`            // "monthly", "forever", "daily"
	Limit   int    `json:"limit,omitempty"`   // Limit for this source (-1 for unlimited)
	Used    int    `json:"used,omitempty"`    // Used amount for this source
	Balance int    `json:"balance,omitempty"` // Balance for forever credits (limit - used)
}
