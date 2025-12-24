// Package postgres provides a PostgreSQL implementation of the goquota.Storage interface.
// This implementation uses SQL transactions with SELECT FOR UPDATE for atomic quota operations.
// Rate limiting is handled by an embedded memory storage adapter for performance.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// Storage implements goquota.Storage using PostgreSQL for quotas
// and embedded memory storage for rate limiting
type Storage struct {
	pool   *pgxpool.Pool
	config Config

	// Embedded memory adapter handles rate limiting transparently
	// This satisfies CheckRateLimit and RecordRateLimitRequest
	*memory.Storage

	// stopCleanup cancels the background cleanup goroutine
	stopCleanup func()
}

// Config holds PostgreSQL storage configuration
type Config struct {
	// ConnectionString is the PostgreSQL connection string
	ConnectionString string

	// Pool configuration
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration

	// Cleanup configuration
	CleanupEnabled  bool
	CleanupInterval time.Duration // How often to run cleanup
	RecordTTL       time.Duration // TTL for consumption/refund records
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		CleanupEnabled:  true,
		CleanupInterval: 1 * time.Hour,
		RecordTTL:       7 * 24 * time.Hour, // 7 days default
	}
}

// New creates a new PostgreSQL storage adapter
func New(ctx context.Context, config Config) (*Storage, error) {
	if config.ConnectionString == "" {
		return nil, fmt.Errorf("connection string is required")
	}

	// Parse connection string
	poolConfig, err := pgxpool.ParseConfig(config.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	// Apply pool settings
	if config.MaxConns > 0 {
		poolConfig.MaxConns = config.MaxConns
	}
	if config.MinConns > 0 {
		poolConfig.MinConns = config.MinConns
	}
	if config.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = config.MaxConnLifetime
	}
	if config.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = config.MaxConnIdleTime
	}

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Initialize embedded memory adapter for rate limiting
	memStorage := memory.New()

	// Create context for background cleanup worker
	cleanupCtx, cancel := context.WithCancel(context.Background())

	s := &Storage{
		pool:        pool,
		config:      config,
		Storage:     memStorage, // Embedded - rate limit methods work automatically
		stopCleanup: cancel,
	}

	// Start cleanup goroutine if enabled
	if config.CleanupEnabled {
		go s.startCleanup(cleanupCtx)
	}

	return s, nil
}

// Close closes the PostgreSQL connection pool and stops background cleanup
func (s *Storage) Close() {
	if s.stopCleanup != nil {
		s.stopCleanup() // Stop the background cleanup routine
	}
	if s.pool != nil {
		s.pool.Close() // Close PG connection pool
	}
}

// GetEntitlement implements goquota.Storage
func (s *Storage) GetEntitlement(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	var ent goquota.Entitlement
	var expiresAt *time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT user_id, tier_id, subscription_start, expires_at, updated_at
			FROM entitlements WHERE user_id = $1`,
		userID).Scan(
		&ent.UserID,
		&ent.Tier,
		&ent.SubscriptionStartDate,
		&expiresAt,
		&ent.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, goquota.ErrEntitlementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entitlement: %w", err)
	}

	ent.ExpiresAt = expiresAt
	return &ent, nil
}

// SetEntitlement implements goquota.Storage
func (s *Storage) SetEntitlement(ctx context.Context, ent *goquota.Entitlement) error {
	if ent == nil || ent.UserID == "" {
		return fmt.Errorf("invalid entitlement")
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO entitlements (user_id, tier_id, subscription_start, expires_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (user_id) DO UPDATE SET
				tier_id = EXCLUDED.tier_id,
				subscription_start = EXCLUDED.subscription_start,
				expires_at = EXCLUDED.expires_at,
				updated_at = EXCLUDED.updated_at`,
		ent.UserID, ent.Tier, ent.SubscriptionStartDate, ent.ExpiresAt, time.Now().UTC(),
	)

	if err != nil {
		return fmt.Errorf("failed to set entitlement: %w", err)
	}

	return nil
}

// GetUsage implements goquota.Storage
func (s *Storage) GetUsage(
	ctx context.Context, userID, resource string, period goquota.Period,
) (*goquota.Usage, error) {
	var usage goquota.Usage
	var periodEnd *time.Time

	err := s.pool.QueryRow(ctx,
		`SELECT user_id, resource, usage_amount, limit_amount, period_start, period_end, period_type, tier, updated_at
			FROM quota_usage
			WHERE user_id = $1 AND resource = $2 AND period_start = $3`,
		userID, resource, period.Start).Scan(
		&usage.UserID,
		&usage.Resource,
		&usage.Used,
		&usage.Limit,
		&usage.Period.Start,
		&periodEnd,
		&usage.Period.Type,
		&usage.Tier,
		&usage.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, nil // No usage yet is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Handle NULL period_end for forever periods
	if periodEnd != nil {
		usage.Period.End = *periodEnd
	} else {
		// For forever periods, set End to a far-future sentinel value
		usage.Period.End = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	return &usage, nil
}

// SetUsage implements goquota.Storage
func (s *Storage) SetUsage(
	ctx context.Context, userID, resource string, usage *goquota.Usage, period goquota.Period,
) error {
	if usage == nil {
		return fmt.Errorf("usage is required")
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO quota_usage 
				(user_id, resource, period_start, period_end, period_type, usage_amount, limit_amount, tier, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, resource, period_start) DO UPDATE SET
				usage_amount = EXCLUDED.usage_amount,
				limit_amount = EXCLUDED.limit_amount,
				tier = EXCLUDED.tier,
				updated_at = EXCLUDED.updated_at`,
		userID, resource, period.Start, period.End, string(period.Type),
		usage.Used, usage.Limit, usage.Tier, time.Now().UTC(),
	)

	if err != nil {
		return fmt.Errorf("failed to set usage: %w", err)
	}

	return nil
}

// ConsumeQuota implements goquota.Storage with atomic consumption via transaction
//
//nolint:gocyclo // Complex function handles transaction, idempotency, and quota checks
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	// Start transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		//nolint:errcheck // Rollback error is safe to ignore if transaction was committed
		_ = tx.Rollback(ctx)
	}()

	// Check idempotency (scoped to user_id) with row-level lock
	// This prevents race conditions where multiple transactions check simultaneously
	if req.IdempotencyKey != "" {
		var existingNewUsed int64
		err := tx.QueryRow(ctx,
			`SELECT new_used FROM consumption_records 
				WHERE user_id = $1 AND consumption_id = $2
				FOR UPDATE`,
			req.UserID, req.IdempotencyKey).Scan(&existingNewUsed)

		if err == nil {
			// Idempotent - return cached result
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return 0, fmt.Errorf("failed to commit: %w", commitErr)
			}
			return int(existingNewUsed), nil
		}
		if err != pgx.ErrNoRows {
			return 0, fmt.Errorf("failed to check idempotency: %w", err)
		}
		// Record doesn't exist yet - proceed with consumption
		// The unique constraint will prevent duplicate inserts later
	}

	// Tip 1: Use UPSERT to avoid insert race condition
	// Ensure row exists (creates if missing, does nothing if present)
	_, err = tx.Exec(ctx,
		`INSERT INTO quota_usage 
				(user_id, resource, period_start, period_end, period_type, usage_amount, limit_amount, tier, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, resource, period_start) DO NOTHING`,
		req.UserID, req.Resource, req.Period.Start, req.Period.End,
		string(req.Period.Type), 0, req.Limit, req.Tier, time.Now().UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to ensure usage record exists: %w", err)
	}

	// Now perform SELECT FOR UPDATE (row is guaranteed to exist)
	var currentUsed int64
	var limitAmount int64
	err = tx.QueryRow(ctx,
		`SELECT usage_amount, limit_amount 
			FROM quota_usage 
			WHERE user_id = $1 AND resource = $2 AND period_start = $3
			FOR UPDATE`,
		req.UserID, req.Resource, req.Period.Start).Scan(&currentUsed, &limitAmount)

	if err != nil {
		return 0, fmt.Errorf("failed to get usage for update: %w", err)
	}

	// Check quota
	newUsed := currentUsed + int64(req.Amount)
	if newUsed > limitAmount {
		return int(currentUsed), goquota.ErrQuotaExceeded
	}

	// Update usage
	_, err = tx.Exec(ctx,
		`UPDATE quota_usage 
			SET usage_amount = $1, updated_at = NOW()
			WHERE user_id = $2 AND resource = $3 AND period_start = $4`,
		newUsed, req.UserID, req.Resource, req.Period.Start)
	if err != nil {
		return 0, fmt.Errorf("failed to update usage: %w", err)
	}

	// Record consumption for idempotency
	// Check again after consumption in case another transaction inserted it
	// This handles the race condition where two transactions both pass the initial check
	if req.IdempotencyKey != "" {
		// Double-check idempotency after consumption (another transaction might have inserted)
		var existingNewUsed int64
		err := tx.QueryRow(ctx,
			`SELECT new_used FROM consumption_records 
				WHERE user_id = $1 AND consumption_id = $2
				FOR UPDATE`,
			req.UserID, req.IdempotencyKey).Scan(&existingNewUsed)

		if err == nil {
			// Another transaction already processed this - rollback and return cached value
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				return 0, fmt.Errorf("failed to rollback: %w", rollbackErr)
			}
			return int(existingNewUsed), nil
		}
		if err != pgx.ErrNoRows {
			return 0, fmt.Errorf("failed to re-check idempotency: %w", err)
		}

		// Still doesn't exist - safe to insert
		expiresAt := time.Now().UTC().Add(s.config.RecordTTL)
		if req.IdempotencyKeyTTL > 0 {
			expiresAt = time.Now().UTC().Add(req.IdempotencyKeyTTL)
		}

		// Note: ConsumeRequest doesn't have Metadata field, so we pass NULL
		// Use NULL for empty metadata (JSONB column requires valid JSON or NULL)
		_, err = tx.Exec(ctx,
			`INSERT INTO consumption_records 
				(consumption_id, user_id, resource, amount, period_start, 
				period_end, period_type, new_used, expires_at, metadata)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULL)
				ON CONFLICT (user_id, consumption_id) DO NOTHING`,
			req.IdempotencyKey, req.UserID, req.Resource, req.Amount,
			req.Period.Start, req.Period.End, string(req.Period.Type),
			newUsed, expiresAt)
		if err != nil {
			return 0, fmt.Errorf("failed to record consumption: %w", err)
		}
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return int(newUsed), nil
}

// RefundQuota implements goquota.Storage
//
//nolint:gocyclo // Complex function handles transaction, idempotency, and period calculation
func (s *Storage) RefundQuota(ctx context.Context, req *goquota.RefundRequest) error {
	if req.Amount < 0 {
		return goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	// Calculate period if not provided
	var period goquota.Period
	if !req.Period.Start.IsZero() {
		period = req.Period
	} else {
		now := time.Now().UTC()
		switch req.PeriodType {
		case goquota.PeriodTypeMonthly:
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

	// Start transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		//nolint:errcheck // Rollback error is safe to ignore if transaction was committed
		_ = tx.Rollback(ctx)
	}()

	// Check idempotency (scoped to user_id)
	if req.IdempotencyKey != "" {
		var exists bool
		err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM refund_records 
				WHERE user_id = $1 AND refund_id = $2)`,
			req.UserID, req.IdempotencyKey).Scan(&exists)

		if err != nil {
			return fmt.Errorf("failed to check idempotency: %w", err)
		}
		if exists {
			// Idempotent - already processed
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return fmt.Errorf("failed to commit: %w", commitErr)
			}
			return nil
		}
	}

	// Get current usage with lock
	var currentUsed int64
	err = tx.QueryRow(ctx,
		`SELECT usage_amount 
			FROM quota_usage 
			WHERE user_id = $1 AND resource = $2 AND period_start = $3
			FOR UPDATE`,
		req.UserID, req.Resource, period.Start).Scan(&currentUsed)

	if err == pgx.ErrNoRows {
		// No usage to refund - this is not an error
		// But we should still record the refund for idempotency
		if req.IdempotencyKey != "" {
			expiresAt := time.Now().UTC().Add(s.config.RecordTTL)
			if req.IdempotencyKeyTTL > 0 {
				expiresAt = time.Now().UTC().Add(req.IdempotencyKeyTTL)
			}

			// Marshal metadata and cast to string for JSONB column (pgx requires string, not []byte for JSONB)
			// Use NULL if metadata is empty (JSONB column requires valid JSON or NULL)
			var metadataVal interface{}
			if len(req.Metadata) > 0 {
				metadataJSON, err := json.Marshal(req.Metadata)
				if err == nil {
					metadataVal = string(metadataJSON)
				} else {
					metadataVal = nil
				}
			}

			_, err = tx.Exec(ctx,
				`INSERT INTO refund_records 
				 (refund_id, user_id, resource, amount, period_start, 
				  period_end, period_type, expires_at, reason, metadata)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				 ON CONFLICT (user_id, refund_id) DO NOTHING`,
				req.IdempotencyKey, req.UserID, req.Resource, req.Amount,
				period.Start, period.End, string(period.Type),
				expiresAt, req.Reason, metadataVal)
			if err != nil {
				return fmt.Errorf("failed to record refund: %w", err)
			}
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("failed to commit: %w", commitErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get usage: %w", err)
	}

	// Refund the quota (decrease used amount, floor at 0)
	newUsed := currentUsed - int64(req.Amount)
	if newUsed < 0 {
		newUsed = 0
	}

	// Update usage
	_, err = tx.Exec(ctx,
		`UPDATE quota_usage 
			SET usage_amount = $1, updated_at = NOW()
			WHERE user_id = $2 AND resource = $3 AND period_start = $4`,
		newUsed, req.UserID, req.Resource, period.Start)
	if err != nil {
		return fmt.Errorf("failed to update usage: %w", err)
	}

	// Record refund for idempotency
	if req.IdempotencyKey != "" {
		expiresAt := time.Now().UTC().Add(s.config.RecordTTL)
		if req.IdempotencyKeyTTL > 0 {
			expiresAt = time.Now().UTC().Add(req.IdempotencyKeyTTL)
		}

		// Marshal metadata and cast to string for JSONB column (pgx requires string, not []byte for JSONB)
		// Use NULL if metadata is empty (JSONB column requires valid JSON or NULL)
		var metadataVal interface{}
		if len(req.Metadata) > 0 {
			metadataJSON, err := json.Marshal(req.Metadata)
			if err == nil {
				metadataVal = string(metadataJSON)
			} else {
				metadataVal = nil
			}
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO refund_records 
			 (refund_id, user_id, resource, amount, period_start, 
			  period_end, period_type, expires_at, reason, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (user_id, refund_id) DO NOTHING`,
			req.IdempotencyKey, req.UserID, req.Resource, req.Amount,
			period.Start, period.End, string(period.Type),
			expiresAt, req.Reason, metadataVal)
		if err != nil {
			return fmt.Errorf("failed to record refund: %w", err)
		}
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

// ApplyTierChange implements goquota.Storage
func (s *Storage) ApplyTierChange(ctx context.Context, req *goquota.TierChangeRequest) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE quota_usage 
			SET limit_amount = $1, tier = $2, updated_at = NOW()
			WHERE user_id = $3 AND resource = $4 AND period_start = $5`,
		req.NewLimit, req.NewTier, req.UserID, req.Resource, req.Period.Start)

	if err != nil {
		return fmt.Errorf("failed to apply tier change: %w", err)
	}

	return nil
}

// GetRefundRecord implements goquota.Storage
//
// IMPORTANT: This method queries by idempotency key only. Since idempotency keys
// are scoped to user_id (UNIQUE(user_id, refund_id)), multiple users can have
// the same key. This query returns the most recent record with the given key
// (ORDER BY timestamp DESC). The caller (Manager) must verify that
// record.UserID matches the expected user to prevent security issues.
func (s *Storage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}

	var record goquota.RefundRecord
	var metadataJSON []byte

	err := s.pool.QueryRow(ctx,
		`SELECT refund_id, user_id, resource, amount, period_start, period_end, 
					period_type, timestamp, reason, metadata
			FROM refund_records
			WHERE refund_id = $1
			ORDER BY timestamp DESC
			LIMIT 1`,
		idempotencyKey).Scan(
		&record.RefundID,
		&record.UserID,
		&record.Resource,
		&record.Amount,
		&record.Period.Start,
		&record.Period.End,
		&record.Period.Type,
		&record.Timestamp,
		&record.Reason,
		&metadataJSON,
	)

	if err == pgx.ErrNoRows {
		return nil, nil // No record found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get refund record: %w", err)
	}

	record.IdempotencyKey = idempotencyKey
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
			// Metadata parsing error is not critical
			record.Metadata = nil
		}
	}

	return &record, nil
}

// GetConsumptionRecord implements goquota.Storage
//
// IMPORTANT: This method queries by idempotency key only. Since idempotency keys
// are scoped to user_id (UNIQUE(user_id, consumption_id)), multiple users can have
// the same key. This query returns the most recent record with the given key
// (ORDER BY timestamp DESC). The caller (Manager) must verify that
// record.UserID matches the expected user to prevent security issues.
func (s *Storage) GetConsumptionRecord(ctx context.Context, idempotencyKey string) (*goquota.ConsumptionRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}

	var record goquota.ConsumptionRecord
	var metadataJSON []byte

	err := s.pool.QueryRow(ctx,
		`SELECT consumption_id, user_id, resource, amount, period_start, period_end,
					period_type, new_used, timestamp, metadata
			FROM consumption_records
			WHERE consumption_id = $1
			ORDER BY timestamp DESC
			LIMIT 1`,
		idempotencyKey).Scan(
		&record.ConsumptionID,
		&record.UserID,
		&record.Resource,
		&record.Amount,
		&record.Period.Start,
		&record.Period.End,
		&record.Period.Type,
		&record.NewUsed,
		&record.Timestamp,
		&metadataJSON,
	)

	if err == pgx.ErrNoRows {
		return nil, nil // No record found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get consumption record: %w", err)
	}

	record.IdempotencyKey = idempotencyKey
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
			// Metadata parsing error is not critical
			record.Metadata = nil
		}
	}

	return &record, nil
}

// CheckRateLimit and RecordRateLimitRequest are automatically handled
// by the embedded memory.Storage - no explicit implementation needed

// startCleanup runs periodic cleanup of expired records
// Tip 2: Uses a dedicated context that can be canceled via Close()
func (s *Storage) startCleanup(ctx context.Context) {
	ticker := time.NewTicker(s.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanupExpiredRecords(context.Background()); err != nil {
				// Log error but continue cleanup loop
				// In production, you might want to use a logger here
				_ = err
			}
		}
	}
}

// cleanupExpiredRecords deletes expired consumption and refund records
func (s *Storage) cleanupExpiredRecords(ctx context.Context) error {
	now := time.Now().UTC()

	// Delete expired consumption records
	_, err := s.pool.Exec(ctx,
		`DELETE FROM consumption_records WHERE expires_at < $1`, now)
	if err != nil {
		return fmt.Errorf("failed to cleanup consumption records: %w", err)
	}

	// Delete expired refund records
	_, err = s.pool.Exec(ctx,
		`DELETE FROM refund_records WHERE expires_at < $1`, now)
	if err != nil {
		return fmt.Errorf("failed to cleanup refund records: %w", err)
	}

	return nil
}

// Cleanup can be called manually to clean up expired records
func (s *Storage) Cleanup(ctx context.Context) error {
	return s.cleanupExpiredRecords(ctx)
}

// Ping checks the PostgreSQL connection
func (s *Storage) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// AddLimit implements goquota.Storage
func (s *Storage) AddLimit(
	ctx context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	// Use transaction to ensure idempotency check and limit increment are atomic
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		//nolint:errcheck // Rollback error is safe to ignore if transaction was committed
		_ = tx.Rollback(ctx)
	}()

	// 1. Check idempotency INSIDE transaction (if key provided)
	if idempotencyKey != "" {
		var existingID string
		err := tx.QueryRow(ctx, `
			INSERT INTO top_up_records (id, user_id, resource, amount, period_start, period_end, period_type, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (id) DO NOTHING
			RETURNING id
		`, idempotencyKey, userID, resource, amount, period.Start, period.End, string(period.Type)).Scan(&existingID)

		if err == pgx.ErrNoRows {
			// Idempotency key already exists - operation already processed
			return goquota.ErrIdempotencyKeyExists
		}
		if err != nil {
			return fmt.Errorf("failed to check idempotency: %w", err)
		}
		// existingID is empty means this is a new record - proceed
	}

	// 2. Apply limit increment atomically
	_, err = tx.Exec(ctx, `
		INSERT INTO quota_usage (
			user_id, resource, period_start, period_end, period_type, usage_amount, limit_amount, tier, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7, NOW())
		ON CONFLICT (user_id, resource, period_start) 
		DO UPDATE SET limit_amount = quota_usage.limit_amount + $6, updated_at = NOW()
	`, userID, resource, period.Start, period.End, string(period.Type), amount, "default")
	if err != nil {
		return fmt.Errorf("failed to increment limit: %w", err)
	}

	// 3. Commit transaction
	return tx.Commit(ctx)
}

// SubtractLimit implements goquota.Storage
func (s *Storage) SubtractLimit(
	ctx context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	// Use transaction to ensure idempotency check and limit decrement are atomic
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		//nolint:errcheck // Rollback error is safe to ignore if transaction was committed
		_ = tx.Rollback(ctx)
	}()

	// 1. Check idempotency INSIDE transaction (if key provided)
	if idempotencyKey != "" {
		var existingID string
		err := tx.QueryRow(ctx, `
			INSERT INTO refund_records (
				refund_id, user_id, resource, amount, period_start, period_end, period_type, timestamp, expires_at, reason, metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW() + INTERVAL '24 hours', '', NULL)
			ON CONFLICT (user_id, refund_id) DO NOTHING
			RETURNING refund_id
		`, idempotencyKey, userID, resource, amount, period.Start, period.End, string(period.Type)).Scan(&existingID)

		if err == pgx.ErrNoRows {
			// Idempotency key already exists - operation already processed
			return goquota.ErrIdempotencyKeyExists
		}
		if err != nil {
			return fmt.Errorf("failed to check idempotency: %w", err)
		}
	}

	// 2. Apply limit decrement atomically with clamp to 0
	_, err = tx.Exec(ctx, `
		UPDATE quota_usage 
		SET limit_amount = GREATEST(0, limit_amount - $1), updated_at = NOW()
		WHERE user_id = $2 AND resource = $3 AND period_start = $4
	`, amount, userID, resource, period.Start)
	if err != nil {
		return fmt.Errorf("failed to decrement limit: %w", err)
	}

	// 3. Commit transaction
	return tx.Commit(ctx)
}
