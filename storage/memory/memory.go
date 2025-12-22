// Package memory provides an in-memory implementation of the goquota.Storage interface.
// This implementation is primarily intended for testing and development.
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Storage implements goquota.Storage using in-memory maps
type Storage struct {
	mu           sync.RWMutex
	entitlements map[string]*goquota.Entitlement
	usage        map[string]*goquota.Usage
	refunds      map[string]*goquota.RefundRecord // keyed by idempotency key
}

// New creates a new in-memory storage adapter
func New() *Storage {
	return &Storage{
		entitlements: make(map[string]*goquota.Entitlement),
		usage:        make(map[string]*goquota.Usage),
		refunds:      make(map[string]*goquota.RefundRecord),
	}
}

// GetEntitlement implements goquota.Storage
func (s *Storage) GetEntitlement(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ent, ok := s.entitlements[userID]
	if !ok {
		return nil, goquota.ErrEntitlementNotFound
	}

	// Return a copy to prevent external mutations
	entCopy := *ent
	return &entCopy, nil
}

// SetEntitlement implements goquota.Storage
func (s *Storage) SetEntitlement(ctx context.Context, ent *goquota.Entitlement) error {
	if ent == nil || ent.UserID == "" {
		return fmt.Errorf("invalid entitlement")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store a copy to prevent external mutations
	entCopy := *ent
	s.entitlements[ent.UserID] = &entCopy
	return nil
}

// GetUsage implements goquota.Storage
func (s *Storage) GetUsage(ctx context.Context, userID, resource string, period goquota.Period) (*goquota.Usage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := usageKey(userID, resource, period)
	usage, ok := s.usage[key]
	if !ok {
		return nil, nil // No usage yet is not an error
	}

	// Return a copy
	usageCopy := *usage
	return &usageCopy, nil
}

// ConsumeQuota implements goquota.Storage with transaction-safe consumption
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := usageKey(req.UserID, req.Resource, req.Period)
	usage, ok := s.usage[key]

	currentUsed := 0
	if ok {
		currentUsed = usage.Used
	}

	newUsed := currentUsed + req.Amount
	if newUsed > req.Limit {
		return currentUsed, goquota.ErrQuotaExceeded
	}

	// Update or create usage
	s.usage[key] = &goquota.Usage{
		UserID:    req.UserID,
		Resource:  req.Resource,
		Used:      newUsed,
		Limit:     req.Limit,
		Period:    req.Period,
		Tier:      req.Tier,
		UpdatedAt: time.Now().UTC(),
	}

	return newUsed, nil
}

// ApplyTierChange implements goquota.Storage
func (s *Storage) ApplyTierChange(ctx context.Context, req *goquota.TierChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// For in-memory implementation, we just update the limit
	key := usageKey(req.UserID, req.Resource, req.Period)

	usage, ok := s.usage[key]
	if !ok {
		// Create new usage with new limit
		s.usage[key] = &goquota.Usage{
			UserID:    req.UserID,
			Resource:  req.Resource,
			Used:      req.CurrentUsed,
			Limit:     req.NewLimit,
			Period:    req.Period,
			Tier:      req.NewTier,
			UpdatedAt: time.Now().UTC(),
		}
		return nil
	}

	// Update existing usage
	usage.Limit = req.NewLimit
	usage.Tier = req.NewTier
	usage.UpdatedAt = time.Now().UTC()

	return nil
}

// usageKey generates a unique key for usage tracking
func usageKey(userID, resource string, period goquota.Period) string {
	return fmt.Sprintf("%s:%s:%s", userID, resource, period.Key())
}

// SetUsage implements goquota.Storage
func (s *Storage) SetUsage(ctx context.Context, userID, resource string, usage *goquota.Usage, period goquota.Period) error {
	if usage == nil {
		return fmt.Errorf("usage is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := usageKey(userID, resource, period)
	usageCopy := *usage
	s.usage[key] = &usageCopy
	return nil
}

// RefundQuota implements goquota.Storage
func (s *Storage) RefundQuota(ctx context.Context, req *goquota.RefundRequest) error {
	if req.Amount < 0 {
		return goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate refund
	if req.IdempotencyKey != "" {
		if _, exists := s.refunds[req.IdempotencyKey]; exists {
			// Duplicate refund - return success (idempotent)
			return nil
		}
	}

	// Calculate period
	var period goquota.Period
	// Use period from request if available (populated by Manager)
	if !req.Period.Start.IsZero() {
		period = req.Period
	} else {
		// Fallback for direct usage (though Manager always sets it)
		now := time.Now().UTC()
		switch req.PeriodType {
		case goquota.PeriodTypeMonthly:
			// Use simple monthly period calculation
			start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, 0)
			period = goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeMonthly}
		case goquota.PeriodTypeDaily:
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			end := start.Add(24 * time.Hour)
			period = goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeDaily}
		default:
			return goquota.ErrInvalidPeriod
		}
	}

	key := usageKey(req.UserID, req.Resource, period)
	usage, ok := s.usage[key]

	if !ok {
		// No usage to refund - this is not an error
		return nil
	}

	// Refund the quota (decrease used amount)
	newUsed := usage.Used - req.Amount
	if newUsed < 0 {
		newUsed = 0
	}

	usage.Used = newUsed
	usage.UpdatedAt = time.Now().UTC()

	// Store refund record for audit trail
	if req.IdempotencyKey != "" {
		record := &goquota.RefundRecord{
			RefundID:       req.IdempotencyKey, // Using idempotency key as refund ID
			UserID:         req.UserID,
			Resource:       req.Resource,
			Amount:         req.Amount,
			Period:         period,
			Timestamp:      time.Now().UTC(),
			IdempotencyKey: req.IdempotencyKey,
			Reason:         req.Reason,
			Metadata:       req.Metadata,
		}
		s.refunds[req.IdempotencyKey] = record
	}

	return nil
}

// GetRefundRecord implements goquota.Storage
func (s *Storage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.refunds[idempotencyKey]
	if !ok {
		return nil, nil // No record found is not an error
	}

	// Return a copy
	recordCopy := *record
	return &recordCopy, nil
}

// Clear removes all data (useful for testing)
func (s *Storage) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entitlements = make(map[string]*goquota.Entitlement)
	s.usage = make(map[string]*goquota.Usage)
	s.refunds = make(map[string]*goquota.RefundRecord)
	return nil
}
