package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var _ goquota.PromotionStore = (*Storage)(nil)

// ensurePromotionColumn adds the JSONB promotion column used by tier
// promotions. It is idempotent and safe to run on every startup.
func (s *Storage) ensurePromotionColumn(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE %s ADD COLUMN IF NOT EXISTS promotion JSONB`, s.config.EntitlementsTable))
	if err != nil {
		return fmt.Errorf("ensure promotion column: %w", err)
	}
	return nil
}

// marshalPromotion serializes a promotion for storage. A nil promotion yields a
// nil slice, which pgx sends as SQL NULL (used to preserve an existing value on
// base entitlement writes).
func marshalPromotion(p *goquota.TierPromotion) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	return json.Marshal(p)
}

// unmarshalPromotion deserializes a promotion. Empty or unusable payloads
// (cleared column) yield nil.
func unmarshalPromotion(raw []byte) (*goquota.TierPromotion, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var p goquota.TierPromotion
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal promotion: %w", err)
	}
	if p.Tier == "" || p.ExpiresAt.IsZero() {
		return nil, nil
	}
	return &p, nil
}

// SetPromotion implements goquota.PromotionStore.
func (s *Storage) SetPromotion(
	ctx context.Context, userID string, promo *goquota.TierPromotion,
) error {
	if userID == "" {
		return fmt.Errorf("userID is required")
	}
	if promo == nil {
		return fmt.Errorf("promotion is required")
	}
	if promo.ExpiresAt.IsZero() {
		return fmt.Errorf("promotion expiresAt is required")
	}

	raw, err := json.Marshal(promo)
	if err != nil {
		return fmt.Errorf("marshal promotion: %w", err)
	}

	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET promotion = $1, updated_at = NOW() WHERE user_id = $2`,
		s.config.EntitlementsTable), raw, userID)
	if err != nil {
		return fmt.Errorf("failed to set promotion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return goquota.ErrEntitlementNotFound
	}
	return nil
}

// ClearPromotion implements goquota.PromotionStore. It is idempotent.
func (s *Storage) ClearPromotion(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("userID is required")
	}

	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET promotion = NULL, updated_at = NOW() WHERE user_id = $1`,
		s.config.EntitlementsTable), userID)
	if err != nil {
		return fmt.Errorf("failed to clear promotion: %w", err)
	}
	return nil
}
