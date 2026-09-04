package goquota

import (
	"context"
	"fmt"
	"time"
)

func periodForType(now time.Time, ent *Entitlement, periodType PeriodType) (Period, error) {
	switch periodType {
	case PeriodTypeMonthly:
		var start, end time.Time
		// Match GetQuota/Consume: an existing entitlement (even with a zero
		// SubscriptionStartDate) owns the cycle. Only a missing entitlement
		// falls back to today's start-of-day.
		if ent != nil {
			start, end = CurrentCycleForStart(ent.SubscriptionStartDate, now)
		} else {
			start, end = CurrentCycleForStart(startOfDayUTC(now), now)
		}
		return Period{Start: start, End: end, Type: PeriodTypeMonthly}, nil
	case PeriodTypeDaily:
		return dailyPeriodForEntitlement(now, ent), nil
	case PeriodTypeForever:
		return Period{
			Start: startOfDayUTC(now),
			End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
			Type:  PeriodTypeForever,
		}, nil
	default:
		return Period{}, ErrInvalidPeriod
	}
}

func entitlementOrNil(ent *Entitlement, err error) *Entitlement {
	if err != nil {
		return nil
	}
	return ent
}

func (m *Manager) periodForUser(ctx context.Context, userID string, periodType PeriodType, now time.Time) (Period, error) {
	ent, err := m.GetEntitlement(ctx, userID)
	if err != nil && err != ErrEntitlementNotFound {
		return Period{}, err
	}
	return periodForType(now, entitlementOrNil(ent, err), periodType)
}

func applyDrainToUsage(usage *Usage) {
	usage.Limit = drainLimit(usage.Used)
}

func (m *Manager) drainRemainingFallback(ctx context.Context, userID, resource string, period Period) error {
	usage, err := m.storage.GetUsage(ctx, userID, resource, period)
	if err != nil {
		return fmt.Errorf("drain remaining: get usage: %w", err)
	}
	if usage == nil {
		return nil
	}
	applyDrainToUsage(usage)
	if err := m.storage.SetUsage(ctx, userID, resource, usage, period); err != nil {
		return fmt.Errorf("drain remaining: set usage: %w", err)
	}
	return nil
}

// DrainRemaining sets Limit = Used for one user/resource/period without changing Used.
// It is the single-document primitive for sealing leftover forever credits.
func (m *Manager) DrainRemaining(ctx context.Context, userID, resource string, periodType PeriodType) error {
	if userID == "" || resource == "" {
		return ErrInvalidMergeRequest
	}

	now := m.now(ctx)
	period, err := m.periodForUser(ctx, userID, periodType, now)
	if err != nil {
		return err
	}

	start := time.Now()
	if drainer, ok := m.storage.(RemainingDrainer); ok {
		err = drainer.DrainRemaining(ctx, userID, resource, period)
	} else {
		err = m.drainRemainingFallback(ctx, userID, resource, period)
	}
	m.metrics.RecordStorageOperation("DrainRemaining", time.Since(start), err)
	if err != nil {
		m.logger.Error("failed to drain remaining",
			Field{"userId", userID},
			Field{"resource", resource},
			Field{"periodType", periodType},
			Field{"error", err},
		)
		return err
	}

	m.cache.InvalidateUsage(userID + ":" + resource + ":" + period.Key())
	m.logger.Info("remaining credits drained",
		Field{"userId", userID},
		Field{"resource", resource},
		Field{"periodType", periodType},
	)
	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("%s-%s-drain-%d", userID, resource, now.UnixNano()),
		UserID:    userID,
		Resource:  resource,
		Action:    "drain_remaining",
		Timestamp: now,
		Actor:     "system",
		Reason:    "drain_remaining",
		Metadata: map[string]string{
			"periodType": string(periodType),
		},
	})
	return nil
}

// MergeUser atomically merges source quota into target for the given resources
// and periods. Redis and tiered storage return ErrUnsupportedOperation.
//
// Daily/monthly: target.Used += source.Used; source.Used = 0; limits are not added.
// Forever: target.Limit += remaining; source.Limit = source.Used.
func (m *Manager) MergeUser(ctx context.Context, req *MergeUserRequest) (*MergeUserResult, error) {
	if err := validateMergeUserRequest(req); err != nil {
		return nil, err
	}

	merger, ok := m.storage.(UserMerger)
	if !ok {
		return nil, ErrUnsupportedOperation
	}

	now := m.now(ctx)
	sourceEnt, sourceErr := m.GetEntitlement(ctx, req.SourceUserID)
	if sourceErr != nil && sourceErr != ErrEntitlementNotFound {
		return nil, sourceErr
	}

	targetEnt, targetErr := m.GetEntitlement(ctx, req.TargetUserID)
	if targetErr != nil && targetErr != ErrEntitlementNotFound {
		return nil, targetErr
	}

	items := make([]MergeItem, 0, len(req.Resources)*len(req.Periods))
	for _, resource := range req.Resources {
		for _, periodType := range req.Periods {
			srcPeriod, err := periodForType(now, entitlementOrNil(sourceEnt, sourceErr), periodType)
			if err != nil {
				return nil, err
			}
			dstPeriod, err := periodForType(now, entitlementOrNil(targetEnt, targetErr), periodType)
			if err != nil {
				return nil, err
			}
			items = append(items, MergeItem{
				Resource:     resource,
				PeriodType:   periodType,
				SourcePeriod: srcPeriod,
				TargetPeriod: dstPeriod,
			})
		}
	}

	storageReq := &StorageMergeRequest{
		SourceUserID:   req.SourceUserID,
		TargetUserID:   req.TargetUserID,
		Items:          items,
		IdempotencyKey: req.IdempotencyKey,
		SealSource:     req.SealSource,
		Now:            now,
	}
	if req.SealSource {
		exp := sealExpireAt(now, req.SealTTL)
		storageReq.SealExpireAt = &exp
	}

	start := time.Now()
	result, err := merger.MergeUser(ctx, storageReq)
	m.metrics.RecordStorageOperation("MergeUser", time.Since(start), err)
	if err != nil {
		m.logger.Error("failed to merge user quota",
			Field{"sourceUserId", req.SourceUserID},
			Field{"targetUserId", req.TargetUserID},
			Field{"idempotencyKey", req.IdempotencyKey},
			Field{"error", err},
		)
		return nil, err
	}

	if result != nil && result.IdempotentReplay {
		m.metrics.RecordIdempotencyHit("merge")
	}

	m.cache.InvalidateEntitlement(req.SourceUserID)
	m.cache.InvalidateEntitlement(req.TargetUserID)
	for _, item := range items {
		m.cache.InvalidateUsage(req.SourceUserID + ":" + item.Resource + ":" + item.SourcePeriod.Key())
		m.cache.InvalidateUsage(req.TargetUserID + ":" + item.Resource + ":" + item.TargetPeriod.Key())
	}

	m.logger.Info("user quota merged",
		Field{"sourceUserId", req.SourceUserID},
		Field{"targetUserId", req.TargetUserID},
		Field{"idempotencyKey", req.IdempotencyKey},
		Field{"replay", result != nil && result.IdempotentReplay},
	)
	m.logAuditEntry(ctx, &AuditLogEntry{
		ID:        fmt.Sprintf("merge-%s-%d", req.IdempotencyKey, now.UnixNano()),
		UserID:    req.TargetUserID,
		Action:    "merge_user",
		Timestamp: now,
		Actor:     "system",
		Reason:    "merge_user",
		Metadata: map[string]string{
			"sourceUserId":   req.SourceUserID,
			"idempotencyKey": req.IdempotencyKey,
		},
	})
	return result, nil
}
