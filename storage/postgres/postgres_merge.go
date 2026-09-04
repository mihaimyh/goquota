package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var (
	_ goquota.RemainingDrainer = (*Storage)(nil)
	_ goquota.UserMerger       = (*Storage)(nil)
)

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

func (s *Storage) ensureMergeTables(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			idempotency_key TEXT PRIMARY KEY,
			source_user_id TEXT NOT NULL,
			target_user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			result_json JSONB NOT NULL
		)`, s.config.MergeRecordsTable))
	if err != nil {
		return fmt.Errorf("ensure merge_records: %w", err)
	}
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			user_id TEXT PRIMARY KEY,
			sealed BOOLEAN NOT NULL DEFAULT TRUE,
			migrated_to TEXT NOT NULL,
			expire_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, s.config.IdentitySealsTable))
	if err != nil {
		return fmt.Errorf("ensure identity_seals: %w", err)
	}
	return nil
}

func (s *Storage) ensureUsagePeriodTypeKey(ctx context.Context) error {
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE %s DROP CONSTRAINT IF EXISTS quota_usage_user_id_resource_period_start_key`,
		s.config.UsageTable,
	))
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
		CREATE UNIQUE INDEX IF NOT EXISTS quota_usage_user_resource_type_start_idx
		ON %s (user_id, resource, period_type, period_start)`, s.config.UsageTable))
	if err != nil {
		return fmt.Errorf("ensure usage period_type key: %w", err)
	}
	return nil
}

func (s *Storage) loadIdentitySeal(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	ent := &goquota.Entitlement{UserID: userID}
	if err := s.overlayIdentitySeal(ctx, ent); err != nil {
		return nil, err
	}
	if !ent.Sealed {
		return nil, nil
	}
	return ent, nil
}

func (s *Storage) overlayIdentitySeal(ctx context.Context, ent *goquota.Entitlement) error {
	var sealed bool
	var migratedTo string
	var expireAt *time.Time
	err := s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT sealed, migrated_to, expire_at FROM %s WHERE user_id = $1`,
			s.config.IdentitySealsTable),
		ent.UserID,
	).Scan(&sealed, &migratedTo, &expireAt)
	if err == pgx.ErrNoRows || isUndefinedTable(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get identity seal: %w", err)
	}
	ent.Sealed = sealed
	ent.MigratedTo = migratedTo
	ent.ExpireAt = expireAt
	return nil
}

// DrainRemaining implements goquota.RemainingDrainer.
func (s *Storage) DrainRemaining(ctx context.Context, userID, resource string, period goquota.Period) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		SET limit_amount = GREATEST(0, usage_amount), updated_at = NOW()
		WHERE user_id = $1 AND resource = $2 AND period_start = $3 AND period_type = $4`,
		s.config.UsageTable),
		userID, resource, period.Start, string(period.Type),
	)
	if err != nil {
		return fmt.Errorf("drain remaining: %w", err)
	}
	return nil
}

// MergeUser implements goquota.UserMerger in one SQL transaction.
func (s *Storage) MergeUser(ctx context.Context, req *goquota.StorageMergeRequest) (*goquota.MergeUserResult, error) {
	if req == nil || req.IdempotencyKey == "" {
		return nil, goquota.ErrInvalidMergeRequest
	}
	if req.SourceUserID == req.TargetUserID {
		return nil, goquota.ErrSameUser
	}
	if err := s.ensureMergeTables(ctx); err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("merge user: begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // safe after commit
	}()

	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (idempotency_key, source_user_id, target_user_id, created_at, result_json)
		VALUES ($1, $2, $3, NOW(), '{}'::jsonb)
		ON CONFLICT (idempotency_key) DO NOTHING`, s.config.MergeRecordsTable),
		req.IdempotencyKey, req.SourceUserID, req.TargetUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("merge user: insert record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		result, replayErr := s.loadMergeResult(ctx, tx, req.IdempotencyKey)
		if replayErr != nil {
			return nil, replayErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("merge user: commit replay: %w", err)
		}
		result.IdempotentReplay = true
		return result, nil
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := s.rejectSealedInTx(ctx, tx, req.SourceUserID, now); err != nil {
		return nil, err
	}
	if err := s.rejectSealedInTx(ctx, tx, req.TargetUserID, now); err != nil {
		return nil, err
	}

	if err := s.lockUsageRows(ctx, tx, req); err != nil {
		return nil, err
	}

	result := &goquota.MergeUserResult{Transfers: make([]goquota.MergeTransfer, 0, len(req.Items))}
	for _, item := range req.Items {
		src, err := s.usageFromTx(ctx, tx, req.SourceUserID, item.Resource, item.SourcePeriod)
		if err != nil {
			return nil, err
		}
		dst, err := s.usageFromTx(ctx, tx, req.TargetUserID, item.Resource, item.TargetPeriod)
		if err != nil {
			return nil, err
		}
		pair := goquota.ApplyMergePair(item.PeriodType, src, dst, item.SourcePeriod, item.TargetPeriod, now)
		if pair.WriteSrc {
			pair.Src.UserID = req.SourceUserID
			pair.Src.Resource = item.Resource
			if err := s.upsertUsageTx(ctx, tx, req.SourceUserID, item.Resource, item.SourcePeriod, &pair.Src); err != nil {
				return nil, err
			}
		}
		if pair.WriteDst {
			pair.Dst.UserID = req.TargetUserID
			pair.Dst.Resource = item.Resource
			if err := s.upsertUsageTx(ctx, tx, req.TargetUserID, item.Resource, item.TargetPeriod, &pair.Dst); err != nil {
				return nil, err
			}
		}
		if pair.WriteSrc || pair.WriteDst {
			result.Transfers = append(result.Transfers, goquota.MergeTransfer{
				Resource:   item.Resource,
				PeriodType: item.PeriodType,
				Amount:     pair.Transferred,
			})
		}
	}

	if req.SealSource {
		if err := s.upsertSealTx(ctx, tx, req.SourceUserID, req.TargetUserID, req.SealExpireAt, now); err != nil {
			return nil, err
		}
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("merge user: marshal result: %w", err)
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET result_json = $1 WHERE idempotency_key = $2`,
		s.config.MergeRecordsTable), raw, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("merge user: store result: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("merge user: commit: %w", err)
	}
	return result, nil
}

func (s *Storage) loadMergeResult(ctx context.Context, tx pgx.Tx, key string) (*goquota.MergeUserResult, error) {
	var raw []byte
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT result_json FROM %s WHERE idempotency_key = $1`, s.config.MergeRecordsTable),
		key,
	).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("merge user: load record: %w", err)
	}
	result := &goquota.MergeUserResult{}
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, result); err != nil {
			return nil, fmt.Errorf("merge user: decode record: %w", err)
		}
	}
	return result, nil
}

func (s *Storage) rejectSealedInTx(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	ent := &goquota.Entitlement{UserID: userID}
	var sealed bool
	var migratedTo string
	var expireAt *time.Time
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT sealed, migrated_to, expire_at FROM %s WHERE user_id = $1 FOR UPDATE`,
			s.config.IdentitySealsTable),
		userID,
	).Scan(&sealed, &migratedTo, &expireAt)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("merge user: get seal: %w", err)
	}
	ent.Sealed = sealed
	ent.MigratedTo = migratedTo
	ent.ExpireAt = expireAt
	if ent.IsSealed(now) {
		return goquota.ErrUserSealed
	}
	return nil
}

type usageLockKey struct {
	userID     string
	resource   string
	periodType goquota.PeriodType
	start      time.Time
}

func (s *Storage) lockUsageRows(ctx context.Context, tx pgx.Tx, req *goquota.StorageMergeRequest) error {
	keys := make([]usageLockKey, 0, len(req.Items)*2)
	for _, item := range req.Items {
		keys = append(keys,
			usageLockKey{userID: req.SourceUserID, resource: item.Resource, periodType: item.PeriodType, start: item.SourcePeriod.Start},
			usageLockKey{userID: req.TargetUserID, resource: item.Resource, periodType: item.PeriodType, start: item.TargetPeriod.Start},
		)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].userID != keys[j].userID {
			return keys[i].userID < keys[j].userID
		}
		if keys[i].resource != keys[j].resource {
			return keys[i].resource < keys[j].resource
		}
		if keys[i].periodType != keys[j].periodType {
			return keys[i].periodType < keys[j].periodType
		}
		return keys[i].start.Before(keys[j].start)
	})
	seen := make(map[usageLockKey]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		_, err := tx.Exec(ctx, fmt.Sprintf(`
			SELECT 1 FROM %s
			WHERE user_id = $1 AND resource = $2 AND period_start = $3 AND period_type = $4
			FOR UPDATE`, s.config.UsageTable),
			key.userID, key.resource, key.start, string(key.periodType),
		)
		if err != nil {
			return fmt.Errorf("merge user: lock usage: %w", err)
		}
	}
	return nil
}

func (s *Storage) usageFromTx(
	ctx context.Context, tx pgx.Tx, userID, resource string, period goquota.Period,
) (*goquota.Usage, error) {
	var usage goquota.Usage
	var periodEnd *time.Time
	err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT user_id, resource, usage_amount, limit_amount, period_start, period_end, period_type, tier, updated_at
		FROM %s
		WHERE user_id = $1 AND resource = $2 AND period_start = $3 AND period_type = $4`,
		s.config.UsageTable),
		userID, resource, period.Start, string(period.Type),
	).Scan(
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
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("merge user: get usage: %w", err)
	}
	if periodEnd != nil {
		usage.Period.End = *periodEnd
	}
	return &usage, nil
}

func (s *Storage) upsertUsageTx(
	ctx context.Context, tx pgx.Tx, userID, resource string, period goquota.Period, usage *goquota.Usage,
) error {
	_, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s
			(user_id, resource, period_start, period_end, period_type, usage_amount, limit_amount, tier, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, resource, period_type, period_start) DO UPDATE SET
			usage_amount = EXCLUDED.usage_amount,
			limit_amount = EXCLUDED.limit_amount,
			tier = EXCLUDED.tier,
			updated_at = EXCLUDED.updated_at`, s.config.UsageTable),
		userID, resource, period.Start, period.End, string(period.Type),
		usage.Used, usage.Limit, usage.Tier, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("merge user: upsert usage: %w", err)
	}
	return nil
}

func (s *Storage) upsertSealTx(
	ctx context.Context, tx pgx.Tx, sourceUserID, targetUserID string, expireAt *time.Time, now time.Time,
) error {
	_, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (user_id, sealed, migrated_to, expire_at, updated_at)
		VALUES ($1, TRUE, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			sealed = TRUE,
			migrated_to = EXCLUDED.migrated_to,
			expire_at = EXCLUDED.expire_at,
			updated_at = EXCLUDED.updated_at`, s.config.IdentitySealsTable),
		sourceUserID, targetUserID, expireAt, now,
	)
	if err != nil {
		return fmt.Errorf("merge user: seal source: %w", err)
	}
	return nil
}
