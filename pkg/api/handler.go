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
	statusError   = "error"
)

// Handler provides HTTP endpoints for quota inspection
type Handler struct {
	config Config
}

// GetUsage returns a standardized JSON response of the user's current quota standing
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	var errorType string
	status := "success"

	// Record metrics on exit
	defer func() {
		if h.config.Metrics != nil {
			duration := time.Since(startTime)
			h.config.Metrics.RecordUsageAPIRequestDuration(duration)
			h.config.Metrics.RecordUsageAPIRequest(status, errorType)
		}
	}()

	// 1. Extract and validate User ID
	userID, ok := h.validateUserID(w, r, &status, &errorType)
	if !ok {
		return
	}

	// 2. Get Entitlement (Tier)
	ent, tier, ok := h.getEntitlementAndTier(ctx, userID, &status, &errorType, w, r)
	if !ok {
		return
	}

	// 3. Discover Resources and record metrics
	resources := h.discoverResourcesWithMetrics(ctx, userID)

	// 4. Build response for each resource
	resourceUsage := h.buildResourceUsageMap(ctx, userID, resources, ent, &errorType)

	// 5. Send response
	h.sendUsageResponse(w, userID, tier, status, resourceUsage, &status, &errorType)
}

// validateUserID extracts and validates the user ID from the request
func (h *Handler) validateUserID(w http.ResponseWriter, r *http.Request, status, errorType *string) (string, bool) {
	userID := h.config.GetUserID(r)
	if userID == "" {
		*status = statusError
		*errorType = "auth_error"
		h.handleError(w, r, fmt.Errorf("user ID not found"), http.StatusUnauthorized)
		return "", false
	}

	if len(userID) > maxUserIDLen {
		*status = statusError
		*errorType = "validation_error"
		h.handleError(w, r, fmt.Errorf("invalid user ID format"), http.StatusBadRequest)
		return "", false
	}

	return userID, true
}

// getEntitlementAndTier retrieves entitlement and determines tier/status
func (h *Handler) getEntitlementAndTier(
	ctx context.Context, userID string, status, errorType *string,
	w http.ResponseWriter, r *http.Request,
) (*goquota.Entitlement, string, bool) {
	ent, err := h.config.Manager.GetEntitlement(ctx, userID)
	tier := tierDefault
	*status = statusDefault

	if err == nil && ent != nil {
		tier = ent.Tier
		if ent.ExpiresAt != nil && ent.ExpiresAt.Before(time.Now().UTC()) {
			*status = statusExpired
		} else {
			*status = statusActive
		}
	} else if err != nil && err != goquota.ErrEntitlementNotFound {
		*status = statusError
		*errorType = "storage_error"
		h.handleError(w, r, fmt.Errorf("failed to get entitlement: %w", err), http.StatusInternalServerError)
		return nil, "", false
	}

	return ent, tier, true
}

// discoverResourcesWithMetrics discovers resources and records metrics
func (h *Handler) discoverResourcesWithMetrics(ctx context.Context, userID string) []string {
	totalResources := len(h.config.KnownResources)
	resources := h.discoverResources(ctx, userID)

	if h.config.Metrics != nil && totalResources > 0 {
		filteredCount := len(resources)
		if h.config.ResourceFilter != nil {
			savedCount := totalResources - filteredCount
			if savedCount > 0 {
				h.config.Metrics.RecordResourceFilterQueriesSaved(savedCount)
			}
			ratio := float64(filteredCount) / float64(totalResources)
			h.config.Metrics.RecordResourceFilterEffectivenessRatio(ratio)
			h.config.Metrics.RecordUsageAPIResourceFilterEffectiveness(filteredCount, totalResources)
		}
		h.config.Metrics.RecordUsageAPIResourcesDiscovered(len(resources))
	}

	return resources
}

// buildResourceUsageMap builds the resource usage map
func (h *Handler) buildResourceUsageMap(
	ctx context.Context, userID string, resources []string,
	ent *goquota.Entitlement, errorType *string,
) map[string]ResourceUsage {
	resourceUsage := make(map[string]ResourceUsage)
	for _, resource := range resources {
		usage, err := h.buildResourceUsage(ctx, userID, resource, ent)
		if err != nil {
			if h.config.Metrics != nil && *errorType == "" {
				*errorType = "partial_error"
			}
			continue
		}
		if usage != nil {
			resourceUsage[resource] = *usage
		}
	}
	return resourceUsage
}

// sendUsageResponse sends the usage response
func (h *Handler) sendUsageResponse(
	w http.ResponseWriter, userID, tier, status string,
	resourceUsage map[string]ResourceUsage, finalStatus, errorType *string,
) {
	response := UsageResponse{
		UserID:    userID,
		Tier:      tier,
		Status:    status,
		Resources: resourceUsage,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		if h.config.Metrics != nil {
			*finalStatus = statusError
			*errorType = "encoding_error"
		}
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
	ctx context.Context, userID, resource string, ent *goquota.Entitlement,
) (*ResourceUsage, error) {
	meter, err := h.config.Manager.GetMeterQuota(ctx, userID, resource)
	if err != nil {
		return nil, fmt.Errorf("failed to get meter quota: %w", err)
	}

	var resetAt *time.Time
	if ent != nil {
		period, cycleErr := h.config.Manager.GetCurrentCycle(ctx, userID)
		if cycleErr == nil {
			resetAt = &period.End
		}
	}

	return &ResourceUsage{
		Limit:     meter.Limit,
		Used:      meter.Used,
		Remaining: meter.Remaining,
		ResetAt:   resetAt,
		Breakdown: breakdownFromMeter(meter),
	}, nil
}

func breakdownFromMeter(meter *goquota.MeterQuota) []QuotaBreakdown {
	if meter == nil || len(meter.Periods) == 0 {
		return []QuotaBreakdown{}
	}
	breakdown := make([]QuotaBreakdown, 0, len(meter.Periods))
	for _, period := range meter.Periods {
		item := QuotaBreakdown{
			Source: string(period.Period),
			Limit:  period.Limit,
			Used:   period.Used,
		}
		if period.Period == goquota.PeriodTypeForever {
			item.Balance = period.Remaining
		}
		breakdown = append(breakdown, item)
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
