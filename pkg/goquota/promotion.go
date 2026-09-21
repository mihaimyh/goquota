package goquota

import (
	"context"
	"fmt"
	"time"
)

// effectiveTier resolves the tier that is in effect for an entitlement at now.
//
// An active promotion (now < Promotion.ExpiresAt) overrides the base tier. This
// is evaluated on every read against the current time, so promotions expire
// automatically without a background job and a stale cache self-corrects.
func (m *Manager) effectiveTier(ent *Entitlement, now time.Time) string {
	if ent != nil && ent.Promotion.IsActive(now) {
		return ent.Promotion.Tier
	}
	if ent != nil && ent.Tier != "" {
		return ent.Tier
	}
	return m.config.DefaultTier
}

// tierFor resolves the effective tier for a loaded entitlement without forcing
// a storage clock read unless a promotion is actually present. The vast
// majority of users have no promotion, so this keeps the hot paths free of an
// extra Now() round-trip.
func (m *Manager) tierFor(ctx context.Context, ent *Entitlement) string {
	if ent == nil {
		return m.config.DefaultTier
	}
	if ent.Promotion != nil {
		return m.effectiveTier(ent, m.now(ctx))
	}
	if ent.Tier != "" {
		return ent.Tier
	}
	return m.config.DefaultTier
}

// cloneEntitlement returns a shallow copy of ent (or nil).
func cloneEntitlement(ent *Entitlement) *Entitlement {
	if ent == nil {
		return nil
	}
	cp := *ent
	return &cp
}

// validatePromotionRequest validates a promotion request and resolves its expiry.
func (m *Manager) validatePromotionRequest(req *PromotionRequest, now time.Time) (time.Time, error) {
	if req == nil {
		return time.Time{}, fmt.Errorf("%w: request is required", ErrInvalidPromotionRequest)
	}
	if req.UserID == "" {
		return time.Time{}, fmt.Errorf("%w: userID is required", ErrInvalidPromotionRequest)
	}
	if req.Tier == "" {
		return time.Time{}, fmt.Errorf("%w: tier is required", ErrInvalidPromotionRequest)
	}
	if _, ok := m.config.Tiers[req.Tier]; !ok {
		return time.Time{}, fmt.Errorf(
			"%w: tier %q is not configured", ErrInvalidPromotionRequest, req.Tier)
	}

	expiry := req.ExpiresAt
	if expiry.IsZero() {
		if req.Duration <= 0 {
			return time.Time{}, fmt.Errorf(
				"%w: expiresAt or a positive duration is required", ErrInvalidPromotionRequest)
		}
		expiry = now.Add(req.Duration)
	}
	expiry = expiry.UTC()
	if !expiry.After(now) {
		return time.Time{}, fmt.Errorf(
			"%w: expiresAt must be in the future", ErrInvalidPromotionRequest)
	}
	return expiry, nil
}

// GrantPromotion applies a time-boxed tier promotion to a user.
//
// It writes only the promotion overlay: the base tier, subscription start date,
// base expiry and UpdatedAt are never modified, so billing providers keep full
// ownership of those fields and webhook idempotency is preserved. If the user
// has no entitlement yet, a base entitlement at Config.DefaultTier is created so
// the user falls back to it when the promotion expires.
//
// Repeating a grant with an IdempotencyKey that matches an already-active
// promotion is a no-op (safe retries). Returns ErrUnsupportedOperation when the
// configured storage does not implement PromotionStore.
func (m *Manager) GrantPromotion(ctx context.Context, req *PromotionRequest) (*Entitlement, error) {
	now := m.now(ctx)
	expiresAt, err := m.validatePromotionRequest(req, now)
	if err != nil {
		return nil, err
	}

	store, ok := m.storage.(PromotionStore)
	if !ok {
		return nil, ErrUnsupportedOperation
	}

	ent, err := m.storage.GetEntitlement(ctx, req.UserID)
	switch {
	case err == ErrEntitlementNotFound || (err == nil && ent == nil):
		base := &Entitlement{
			UserID:                req.UserID,
			Tier:                  m.config.DefaultTier,
			SubscriptionStartDate: startOfDayUTC(now),
			UpdatedAt:             now,
		}
		if setErr := m.storage.SetEntitlement(ctx, base); setErr != nil {
			return nil, fmt.Errorf("failed to create base entitlement for promotion: %w", setErr)
		}
		ent = base
	case err != nil:
		return nil, err
	}

	if existing := ent.Promotion; existing != nil &&
		req.IdempotencyKey != "" &&
		existing.IdempotencyKey == req.IdempotencyKey &&
		existing.IsActive(now) {
		m.metrics.RecordIdempotencyHit("grant_promotion")
		m.logger.Info("duplicate promotion grant ignored (idempotent)",
			Field{"userId", req.UserID},
			Field{"tier", req.Tier},
			Field{"idempotencyKey", req.IdempotencyKey},
		)
		return cloneEntitlement(ent), nil
	}

	promo := &TierPromotion{
		Tier:           req.Tier,
		GrantedAt:      now,
		ExpiresAt:      expiresAt,
		Source:         req.Source,
		Reason:         req.Reason,
		IdempotencyKey: req.IdempotencyKey,
	}

	start := time.Now()
	err = store.SetPromotion(ctx, req.UserID, promo)
	m.metrics.RecordStorageOperation("SetPromotion", time.Since(start), err)
	if err != nil {
		m.logger.Error("failed to grant promotion",
			Field{"userId", req.UserID},
			Field{"tier", req.Tier},
			Field{"error", err},
		)
		return nil, err
	}

	// Invalidate the entitlement cache so the promotion takes effect immediately.
	m.cache.InvalidateEntitlement(req.UserID)

	m.logger.Info("tier promotion granted",
		Field{"userId", req.UserID},
		Field{"tier", req.Tier},
		Field{"expiresAt", expiresAt},
		Field{"source", req.Source},
	)

	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("%s-promotion-grant-%d", req.UserID, now.UnixNano()),
		UserID:    req.UserID,
		Resource:  "",
		Action:    "grant_promotion",
		Amount:    0,
		Timestamp: now,
		Actor:     "system",
		Reason:    req.Reason,
		Metadata: map[string]string{
			"tier":           req.Tier,
			"source":         req.Source,
			"expiresAt":      expiresAt.Format(time.RFC3339),
			"idempotencyKey": req.IdempotencyKey,
		},
	})

	out := cloneEntitlement(ent)
	out.Promotion = promo
	return out, nil
}

// RevokePromotion clears a user's active promotion, if any. It is idempotent:
// revoking a user with no promotion (or no entitlement) succeeds. Returns
// ErrUnsupportedOperation when the configured storage does not implement
// PromotionStore.
func (m *Manager) RevokePromotion(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("%w: userID is required", ErrInvalidPromotionRequest)
	}

	store, ok := m.storage.(PromotionStore)
	if !ok {
		return ErrUnsupportedOperation
	}

	start := time.Now()
	err := store.ClearPromotion(ctx, userID)
	m.metrics.RecordStorageOperation("ClearPromotion", time.Since(start), err)
	if err != nil {
		m.logger.Error("failed to revoke promotion",
			Field{"userId", userID},
			Field{"error", err},
		)
		return err
	}

	m.cache.InvalidateEntitlement(userID)

	now := m.now(ctx)
	m.logger.Info("tier promotion revoked", Field{"userId", userID})
	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("%s-promotion-revoke-%d", userID, now.UnixNano()),
		UserID:    userID,
		Action:    "revoke_promotion",
		Timestamp: now,
		Actor:     "system",
		Reason:    "revoke_promotion",
	})

	return nil
}

// GetEffectiveTier returns the tier currently in effect for a user, accounting
// for an active promotion. Users without an entitlement resolve to
// Config.DefaultTier.
func (m *Manager) GetEffectiveTier(ctx context.Context, userID string) (string, error) {
	ent, err := m.GetEntitlement(ctx, userID)
	if err == ErrEntitlementNotFound {
		return m.config.DefaultTier, nil
	}
	if err != nil {
		return "", err
	}
	return m.tierFor(ctx, ent), nil
}
