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
}

// New creates a new in-memory storage adapter
func New() *Storage {
	return &Storage{
		entitlements: make(map[string]*goquota.Entitlement),
		usage:        make(map[string]*goquota.Usage),
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
	key := usageKey(req.UserID, "audio_seconds", req.Period)

	usage, ok := s.usage[key]
	if !ok {
		// Create new usage with new limit
		s.usage[key] = &goquota.Usage{
			UserID:    req.UserID,
			Resource:  "audio_seconds",
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

// Clear removes all data (useful for testing)
func (s *Storage) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entitlements = make(map[string]*goquota.Entitlement)
	s.usage = make(map[string]*goquota.Usage)
	return nil
}
