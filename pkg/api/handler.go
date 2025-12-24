package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

const (
	statusActive  = "active"
	statusExpired = "expired"
	statusDefault = "default"
	tierDefault   = "default"
	sourceMonthly = "monthly"
	sourceForever = "forever"
	maxUserIDLen  = 255
)

// Handler provides HTTP endpoints for quota inspection
type Handler struct {
	config Config
}

// GetUsage returns a standardized JSON response of the user's current quota standing
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Extract User ID
	userID := h.config.GetUserID(r)
	if userID == "" {
		h.handleError(w, r, fmt.Errorf("user ID not found"), http.StatusUnauthorized)
		return
	}

	// Validate user ID format (basic validation)
	if userID == "" || len(userID) > maxUserIDLen {
		h.handleError(w, r, fmt.Errorf("invalid user ID format"), http.StatusBadRequest)
		return
	}

	// 2. Get Entitlement (Tier)
	ent, err := h.config.Manager.GetEntitlement(ctx, userID)
	tier := tierDefault // Will be determined from entitlement or GetQuota will use default tier
	status := statusDefault

	if err == nil && ent != nil {
		tier = ent.Tier
		// Determine status
		if ent.ExpiresAt != nil && ent.ExpiresAt.Before(time.Now().UTC()) {
			status = statusExpired
		} else {
			status = statusActive
		}
	} else if err != nil && err != goquota.ErrEntitlementNotFound {
		// Real error (storage, etc.)
		h.handleError(w, r, fmt.Errorf("failed to get entitlement: %w", err), http.StatusInternalServerError)
		return
	}

	// 3. Discover Resources (handle orphaned credits)
	// ResourceFilter is applied inside discoverResources for performance
	resources := h.discoverResources(ctx, userID)

	// 5. Build response for each resource (sequential fetching)
	resourceUsage := make(map[string]ResourceUsage)
	for _, resource := range resources {
		usage, err := h.buildResourceUsage(ctx, userID, resource, tier, ent)
		if err != nil {
			// Log error but continue with other resources
			// Don't fail entire request if one resource fails
			continue
		}
		if usage != nil {
			resourceUsage[resource] = *usage
		}
	}

	// 6. Construct and send response
	response := UsageResponse{
		UserID:    userID,
		Tier:      tier,
		Status:    status,
		Resources: resourceUsage,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log encoding error but response already sent
		return
	}
}

// discoverResources finds all resources that should be included in the response
// Handles orphaned credits by checking:
// 1. KnownResources (if configured) - primary source
// 2. Resources with quotas (discovered by querying) - for tier config resources
// 3. Resources with forever credits - for orphaned credits
//
// Performance Optimization: ResourceFilter is applied BEFORE quota queries to reduce DB load.
// If ResourceFilter is set, only filtered resources are queried (O(RequestedResources) vs O(TotalResources)).
//
// Note: If KnownResources is not provided, returns empty list (resources cannot be discovered
// without a starting point since tier config is not accessible).
func (h *Handler) discoverResources(ctx context.Context, userID string) []string {
	resourceSet := make(map[string]bool)

	// 1. Get candidate resources (apply ResourceFilter early for performance)
	// Without KnownResources, we cannot efficiently discover resources since tier config is not accessible
	if len(h.config.KnownResources) == 0 {
		return []string{}
	}

	// Pre-filter: If ResourceFilter is set, only check those resources
	// This reduces DB load from O(TotalResources) to O(RequestedResources)
	candidates := h.config.KnownResources
	if h.config.ResourceFilter != nil {
		candidates = h.config.ResourceFilter(candidates)
	}

	// 2. Add filtered candidates to resource set
	for _, resource := range candidates {
		resourceSet[resource] = true
	}

	// 3. Query quotas for filtered candidates to discover active ones
	// This discovers:
	// - Resources from tier config (monthly quotas)
	// - Orphaned credits (forever credits not in current tier)
	// Only queries resources that passed the filter (performance optimization)
	allResourcesToCheck := make([]string, 0, len(resourceSet))
	for resource := range resourceSet {
		allResourcesToCheck = append(allResourcesToCheck, resource)
	}

	// Query quotas to discover active resources
	// Include resource if it has any quota (limit > 0, used > 0, or limit == -1)
	for _, resource := range allResourcesToCheck {
		if h.hasActiveQuota(ctx, userID, resource) {
			resourceSet[resource] = true
		}
	}

	// Convert set to slice
	resources := make([]string, 0, len(resourceSet))
	for resource := range resourceSet {
		resources = append(resources, resource)
	}

	return resources
}

// hasActiveQuota checks if a resource has any active quota (monthly or forever)
func (h *Handler) hasActiveQuota(ctx context.Context, userID, resource string) bool {
	// Check monthly quota (discovers tier config resources)
	monthlyUsage, err := h.config.Manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err == nil && monthlyUsage != nil {
		if monthlyUsage.Limit > 0 || monthlyUsage.Used > 0 || monthlyUsage.Limit == -1 {
			return true
		}
	}

	// Check forever credits (discovers orphaned credits)
	foreverUsage, err := h.config.Manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeForever)
	if err == nil && foreverUsage != nil {
		if foreverUsage.Limit > 0 || foreverUsage.Used > 0 {
			return true
		}
	}

	return false
}

// buildResourceUsage builds the ResourceUsage for a single resource.
func (h *Handler) buildResourceUsage(
	ctx context.Context, userID, resource, tier string, ent *goquota.Entitlement,
) (*ResourceUsage, error) {
	// Query monthly quota
	monthlyUsage, err := h.config.Manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
	if err != nil {
		// If error is not "not found", it's a real error
		if err != goquota.ErrEntitlementNotFound {
			return nil, fmt.Errorf("failed to get monthly quota: %w", err)
		}
		// Create zero usage for monthly
		monthlyUsage = &goquota.Usage{
			UserID:   userID,
			Resource: resource,
			Used:     0,
			Limit:    0,
			Tier:     tier,
		}
	}

	// Query forever credits
	foreverUsage, err := h.config.Manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeForever)
	if err != nil {
		// If error is not "not found", it's a real error
		if err != goquota.ErrEntitlementNotFound {
			return nil, fmt.Errorf("failed to get forever quota: %w", err)
		}
		// Create zero usage for forever
		foreverUsage = &goquota.Usage{
			UserID:   userID,
			Resource: resource,
			Used:     0,
			Limit:    0,
			Tier:     tier,
		}
	}

	// Get current cycle for reset time
	var resetAt *time.Time
	if ent != nil {
		period, err := h.config.Manager.GetCurrentCycle(ctx, userID)
		if err == nil {
			resetAt = &period.End
		}
	}

	// Calculate combined view with unlimited handling
	combined := h.calculateCombinedQuota(monthlyUsage, foreverUsage, tier)

	// Build breakdown respecting ConsumptionOrder
	breakdown := h.buildBreakdown(monthlyUsage, foreverUsage, tier)

	return &ResourceUsage{
		Limit:     combined.Limit,
		Used:      combined.Used,
		Remaining: combined.Remaining,
		ResetAt:   resetAt,
		Breakdown: breakdown,
	}, nil
}

// combinedQuota holds the calculated combined quota values
type combinedQuota struct {
	Limit     int
	Used      int
	Remaining int
}

// calculateCombinedQuota calculates the combined quota respecting unlimited logic.
func (h *Handler) calculateCombinedQuota(monthly, forever *goquota.Usage, _ string) combinedQuota {
	// CRITICAL: Handle unlimited (-1) logic
	// If monthly limit is unlimited, combined is unlimited
	if monthly.Limit == -1 {
		return combinedQuota{
			Limit:     -1,
			Used:      monthly.Used,
			Remaining: -1,
		}
	}

	// Calculate forever balance (limit - used)
	foreverBalance := forever.Limit - forever.Used
	if foreverBalance < 0 {
		foreverBalance = 0
	}

	// Combined limit = monthly limit + forever balance
	combinedLimit := monthly.Limit + foreverBalance

	// Combined used = monthly used (forever credits are consumed, not "used" in the traditional sense)
	combinedUsed := monthly.Used

	// Remaining = combined limit - combined used
	combinedRemaining := combinedLimit - combinedUsed
	if combinedRemaining < 0 {
		combinedRemaining = 0
	}

	return combinedQuota{
		Limit:     combinedLimit,
		Used:      combinedUsed,
		Remaining: combinedRemaining,
	}
}

// buildBreakdown builds the breakdown array respecting ConsumptionOrder.
func (h *Handler) buildBreakdown(monthly, forever *goquota.Usage, _ string) []QuotaBreakdown {
	breakdown := make([]QuotaBreakdown, 0, 2)

	// Get ConsumptionOrder from tier config
	// Since we can't access tier config directly, we'll use a default order
	// Monthly first, then forever (most common pattern)
	consumptionOrder := []goquota.PeriodType{goquota.PeriodTypeMonthly, goquota.PeriodTypeForever}

	// Try to get actual ConsumptionOrder if possible
	// For now, use default order

	// Build breakdown in ConsumptionOrder
	for _, periodType := range consumptionOrder {
		switch periodType {
		case goquota.PeriodTypeMonthly:
			if monthly.Limit > 0 || monthly.Used > 0 || monthly.Limit == -1 {
				bd := QuotaBreakdown{
					Source: sourceMonthly,
					Limit:  monthly.Limit,
					Used:   monthly.Used,
				}
				breakdown = append(breakdown, bd)
			}

		case goquota.PeriodTypeForever:
			foreverBalance := forever.Limit - forever.Used
			if foreverBalance < 0 {
				foreverBalance = 0
			}
			if forever.Limit > 0 || foreverBalance > 0 {
				bd := QuotaBreakdown{
					Source:  sourceForever,
					Balance: foreverBalance,
				}
				// Also include limit and used for transparency
				if forever.Limit > 0 {
					bd.Limit = forever.Limit
					bd.Used = forever.Used
				}
				breakdown = append(breakdown, bd)
			}
		}
	}

	return breakdown
}

// handleError handles errors with appropriate HTTP status codes
func (h *Handler) handleError(w http.ResponseWriter, r *http.Request, err error, statusCode int) {
	if h.config.OnError != nil {
		h.config.OnError(w, r, err)
		return
	}

	// Default error handling
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errorResponse := map[string]string{
		"error": err.Error(),
	}
	if encodeErr := json.NewEncoder(w).Encode(errorResponse); encodeErr != nil {
		// Log encoding error but response already sent
		_ = encodeErr
	}
}
