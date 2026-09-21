-- GoQuota PostgreSQL Storage Schema - Tier Promotions
-- Adds the JSONB overlay column used by temporary tier grants
-- (Manager.GrantPromotion / Manager.RevokePromotion).
--
-- The column is nullable: NULL means "no active promotion". Base entitlement
-- writes (provider webhooks/syncs) preserve a non-NULL value via
-- COALESCE(EXCLUDED.promotion, entitlements.promotion).

ALTER TABLE entitlements
    ADD COLUMN IF NOT EXISTS promotion JSONB;

-- Partial index for admin/analytics queries listing users that currently hold a
-- promotion. Expiry itself is resolved at read time by the library, so no
-- expiry expression index is required (and text -> timestamptz casts are not
-- IMMUTABLE, so they cannot be indexed directly).
CREATE INDEX IF NOT EXISTS idx_entitlements_promotion_present
    ON entitlements (user_id)
    WHERE promotion IS NOT NULL;
