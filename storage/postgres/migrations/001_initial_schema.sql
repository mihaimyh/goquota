-- GoQuota PostgreSQL Storage Schema
-- This schema supports quota tracking with ACID transactions
-- Rate limiting is handled in-memory (not stored in this schema)

-- User entitlements (subscription tiers)
CREATE TABLE entitlements (
    user_id VARCHAR(255) PRIMARY KEY,
    tier_id VARCHAR(50) NOT NULL,
    subscription_start TIMESTAMP WITH TIME ZONE NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_entitlements_tier ON entitlements(tier_id);
CREATE INDEX idx_entitlements_updated ON entitlements(updated_at);

-- Quota usage tracking (monthly/daily periods)
CREATE TABLE quota_usage (
    id SERIAL PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    resource VARCHAR(50) NOT NULL,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    period_type VARCHAR(20) NOT NULL, -- 'daily' or 'monthly'
    usage_amount BIGINT DEFAULT 0,
    limit_amount BIGINT NOT NULL,
    tier VARCHAR(50) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, resource, period_start)
);

CREATE INDEX idx_quota_usage_user_resource ON quota_usage(user_id, resource);
CREATE INDEX idx_quota_usage_period ON quota_usage(period_start, period_end);
CREATE INDEX idx_quota_usage_user_period ON quota_usage(user_id, period_start, period_end);

-- Consumption records (for idempotency and audit)
-- Note: Idempotency keys are scoped to user_id (not globally unique)
CREATE TABLE consumption_records (
    id SERIAL PRIMARY KEY,
    consumption_id VARCHAR(255) NOT NULL, -- idempotency key (scoped to user)
    user_id VARCHAR(255) NOT NULL,
    resource VARCHAR(50) NOT NULL,
    amount BIGINT NOT NULL,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    period_type VARCHAR(20) NOT NULL,
    new_used BIGINT NOT NULL,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL, -- For cleanup
    metadata JSONB,
    UNIQUE(user_id, consumption_id) -- Scoped uniqueness
);

CREATE INDEX idx_consumption_user ON consumption_records(user_id);
CREATE INDEX idx_consumption_id ON consumption_records(consumption_id);
CREATE INDEX idx_consumption_expiry ON consumption_records(expires_at); -- For cleanup

-- Refund records (for idempotency and audit)
-- Note: Idempotency keys are scoped to user_id (not globally unique)
CREATE TABLE refund_records (
    id SERIAL PRIMARY KEY,
    refund_id VARCHAR(255) NOT NULL, -- idempotency key (scoped to user)
    user_id VARCHAR(255) NOT NULL,
    resource VARCHAR(50) NOT NULL,
    amount BIGINT NOT NULL,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    period_type VARCHAR(20) NOT NULL,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL, -- For cleanup
    reason TEXT,
    metadata JSONB,
    UNIQUE(user_id, refund_id) -- Scoped uniqueness
);

CREATE INDEX idx_refund_user ON refund_records(user_id);
CREATE INDEX idx_refund_id ON refund_records(refund_id);
CREATE INDEX idx_refund_expiry ON refund_records(expires_at); -- For cleanup

