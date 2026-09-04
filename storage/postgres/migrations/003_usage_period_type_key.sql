-- Daily and forever periods both use start-of-day as period_start.
-- The original UNIQUE(user_id, resource, period_start) collapsed them into one row.
ALTER TABLE quota_usage DROP CONSTRAINT IF EXISTS quota_usage_user_id_resource_period_start_key;
CREATE UNIQUE INDEX IF NOT EXISTS quota_usage_user_resource_type_start_idx
    ON quota_usage (user_id, resource, period_type, period_start);
