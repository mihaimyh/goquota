package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var _ goquota.RemainingDrainer = (*Storage)(nil)

// DrainRemaining implements goquota.RemainingDrainer.
// MergeUser is intentionally not implemented: Redis cannot atomically commit
// multiple identities across slots.
func (s *Storage) DrainRemaining(ctx context.Context, userID, resource string, period goquota.Period) error {
	usage, err := s.GetUsage(ctx, userID, resource, period)
	if err != nil {
		return fmt.Errorf("drain remaining: get usage: %w", err)
	}
	if usage == nil {
		return nil
	}
	usage.Limit = usage.Used
	if usage.Limit < 0 {
		usage.Limit = 0
	}
	usage.UpdatedAt = time.Now().UTC()
	if err := s.SetUsage(ctx, userID, resource, usage, period); err != nil {
		return fmt.Errorf("drain remaining: set usage: %w", err)
	}
	return nil
}
