package tiered

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var _ goquota.RemainingDrainer = (*Storage)(nil)

// DrainRemaining write-throughs Limit=Used to cold, then best-effort hot.
// MergeUser is intentionally not implemented: hot+cold is not one ACID commit.
func (s *Storage) DrainRemaining(ctx context.Context, userID, resource string, period goquota.Period) error {
	if drainer, ok := s.cold.(goquota.RemainingDrainer); ok {
		if err := drainer.DrainRemaining(ctx, userID, resource, period); err != nil {
			return fmt.Errorf("drain remaining: cold: %w", err)
		}
	} else if err := drainViaSetUsage(ctx, s.cold, userID, resource, period); err != nil {
		return fmt.Errorf("drain remaining: cold: %w", err)
	}

	if drainer, ok := s.hot.(goquota.RemainingDrainer); ok {
		_ = drainer.DrainRemaining(ctx, userID, resource, period) //nolint:errcheck // best effort - cold is source of truth
		return nil
	}
	_ = drainViaSetUsage(ctx, s.hot, userID, resource, period) //nolint:errcheck // best effort - cold is source of truth
	return nil
}

func drainViaSetUsage(ctx context.Context, store goquota.Storage, userID, resource string, period goquota.Period) error {
	usage, err := store.GetUsage(ctx, userID, resource, period)
	if err != nil {
		return err
	}
	if usage == nil {
		return nil
	}
	usage.Limit = usage.Used
	if usage.Limit < 0 {
		usage.Limit = 0
	}
	usage.UpdatedAt = time.Now().UTC()
	return store.SetUsage(ctx, userID, resource, usage, period)
}
