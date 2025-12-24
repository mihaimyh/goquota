-- GoQuota PostgreSQL Storage Schema - Forever Periods Support
-- This migration adds support for non-expiring pre-paid credits (PeriodTypeForever)

-- Make period_end nullable to support forever periods
ALTER TABLE quota_usage ALTER COLUMN period_end DROP NOT NULL;

-- Update consumption_records to allow NULL period_end
ALTER TABLE consumption_records ALTER COLUMN period_end DROP NOT NULL;

-- Update refund_records to allow NULL period_end
ALTER TABLE refund_records ALTER COLUMN period_end DROP NOT NULL;

-- Create top_up_records table for tracking credit purchases (idempotency)
CREATE TABLE top_up_records (
    id VARCHAR(255) PRIMARY KEY, -- idempotency key
    user_id VARCHAR(255) NOT NULL,
    resource VARCHAR(50) NOT NULL,
    amount BIGINT NOT NULL,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE,
    period_type VARCHAR(20) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_topup_user ON top_up_records(user_id);
CREATE INDEX idx_topup_created ON top_up_records(created_at);

-- Add index for forever period queries
CREATE INDEX idx_quota_usage_period_type ON quota_usage(period_type);

