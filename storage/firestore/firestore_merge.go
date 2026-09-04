package firestore

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var (
	_ goquota.RemainingDrainer = (*Storage)(nil)
	_ goquota.UserMerger       = (*Storage)(nil)
)

// DrainRemaining implements goquota.RemainingDrainer in a single-document transaction.
func (s *Storage) DrainRemaining(ctx context.Context, userID, resource string, period goquota.Period) error {
	doc := s.usageDoc(userID, resource, period)
	return s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(doc)
		if err != nil && status.Code(err) != codes.NotFound {
			return fmt.Errorf("drain remaining: get usage: %w", err)
		}
		if err != nil || !snap.Exists() {
			return nil
		}
		usage := usageFromData(userID, resource, period, snap.Data())
		usage.Limit = usage.Used
		if usage.Limit < 0 {
			usage.Limit = 0
		}
		usage.UpdatedAt = time.Now().UTC()
		return tx.Set(doc, usageWriteData(usage, period), firestore.MergeAll)
	})
}

// MergeUser implements goquota.UserMerger in one Firestore transaction.
func (s *Storage) MergeUser(ctx context.Context, req *goquota.StorageMergeRequest) (*goquota.MergeUserResult, error) {
	if req == nil || req.IdempotencyKey == "" {
		return nil, goquota.ErrInvalidMergeRequest
	}
	if req.SourceUserID == req.TargetUserID {
		return nil, goquota.ErrSameUser
	}

	var result *goquota.MergeUserResult
	err := s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		now := req.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}

		mergeRef := s.client.Collection(s.mergeRecordsCollection).Doc(req.IdempotencyKey)
		mergeSnap, err := tx.Get(mergeRef)
		if err != nil && status.Code(err) != codes.NotFound {
			return fmt.Errorf("merge user: get record: %w", err)
		}
		if err == nil && mergeSnap.Exists() {
			decoded, decodeErr := mergeResultFromData(mergeSnap.Data())
			if decodeErr != nil {
				return decodeErr
			}
			decoded.IdempotentReplay = true
			result = decoded
			return nil
		}

		srcEnt, err := s.entitlementFromTx(tx, req.SourceUserID)
		if err != nil && err != goquota.ErrEntitlementNotFound {
			return err
		}
		dstEnt, err := s.entitlementFromTx(tx, req.TargetUserID)
		if err != nil && err != goquota.ErrEntitlementNotFound {
			return err
		}
		if srcEnt.IsSealed(now) || dstEnt.IsSealed(now) {
			return goquota.ErrUserSealed
		}

		type pendingWrite struct {
			ref  *firestore.DocumentRef
			data map[string]interface{}
		}
		writes := make([]pendingWrite, 0, len(req.Items)*2+2)
		transfers := make([]goquota.MergeTransfer, 0, len(req.Items))

		for _, item := range req.Items {
			srcRef := s.usageDoc(req.SourceUserID, item.Resource, item.SourcePeriod)
			dstRef := s.usageDoc(req.TargetUserID, item.Resource, item.TargetPeriod)
			src, err := s.usageFromTx(tx, req.SourceUserID, item.Resource, item.SourcePeriod, srcRef)
			if err != nil {
				return err
			}
			dst, err := s.usageFromTx(tx, req.TargetUserID, item.Resource, item.TargetPeriod, dstRef)
			if err != nil {
				return err
			}
			pair := goquota.ApplyMergePair(item.PeriodType, src, dst, item.SourcePeriod, item.TargetPeriod, now)
			if pair.WriteSrc {
				pair.Src.UserID = req.SourceUserID
				pair.Src.Resource = item.Resource
				writes = append(writes, pendingWrite{ref: srcRef, data: usageWriteData(&pair.Src, item.SourcePeriod)})
			}
			if pair.WriteDst {
				pair.Dst.UserID = req.TargetUserID
				pair.Dst.Resource = item.Resource
				writes = append(writes, pendingWrite{ref: dstRef, data: usageWriteData(&pair.Dst, item.TargetPeriod)})
			}
			if pair.WriteSrc || pair.WriteDst {
				transfers = append(transfers, goquota.MergeTransfer{
					Resource:   item.Resource,
					PeriodType: item.PeriodType,
					Amount:     pair.Transferred,
				})
			}
		}

		result = &goquota.MergeUserResult{Transfers: transfers}

		for _, w := range writes {
			if err := tx.Set(w.ref, w.data, firestore.MergeAll); err != nil {
				return fmt.Errorf("merge user: write usage: %w", err)
			}
		}

		if req.SealSource {
			sealRef := s.client.Collection(s.entitlementsCollection).Doc(req.SourceUserID)
			sealData := map[string]interface{}{
				"sealed":     true,
				"migratedTo": req.TargetUserID,
				"updatedAt":  now,
			}
			if req.SealExpireAt != nil {
				sealData["expireAt"] = *req.SealExpireAt
			}
			if err := tx.Set(sealRef, sealData, firestore.MergeAll); err != nil {
				return fmt.Errorf("merge user: seal source: %w", err)
			}
		}

		if err := tx.Create(mergeRef, mergeResultData(req, result, now)); err != nil {
			return fmt.Errorf("merge user: write record: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Storage) entitlementFromTx(tx *firestore.Transaction, userID string) (*goquota.Entitlement, error) {
	snap, err := tx.Get(s.client.Collection(s.entitlementsCollection).Doc(userID))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, goquota.ErrEntitlementNotFound
		}
		return nil, fmt.Errorf("merge user: get entitlement: %w", err)
	}
	if !snap.Exists() {
		return nil, goquota.ErrEntitlementNotFound
	}
	data := snap.Data()
	ent := &goquota.Entitlement{
		UserID:     userID,
		Tier:       getString(data, "tier"),
		Sealed:     getBool(data, "sealed"),
		MigratedTo: getString(data, "migratedTo"),
	}
	if expireAt := getTime(data, "expireAt"); !expireAt.IsZero() {
		ent.ExpireAt = &expireAt
	}
	return ent, nil
}

func (s *Storage) usageFromTx(
	tx *firestore.Transaction, userID, resource string, period goquota.Period, doc *firestore.DocumentRef,
) (*goquota.Usage, error) {
	snap, err := tx.Get(doc)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("merge user: get usage: %w", err)
	}
	if !snap.Exists() {
		return nil, nil
	}
	return usageFromData(userID, resource, period, snap.Data()), nil
}

func usageFromData(userID, resource string, period goquota.Period, data map[string]interface{}) *goquota.Usage {
	usage := &goquota.Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      getInt(data, "used"),
		Limit:     getInt(data, "limit"),
		Period:    period,
		Tier:      getString(data, "tier"),
		UpdatedAt: getTime(data, "updatedAt"),
	}
	if periodEnd, ok := data["cycleEnd"].(time.Time); ok && !periodEnd.IsZero() {
		usage.Period.End = periodEnd
	}
	return usage
}

func usageWriteData(usage *goquota.Usage, period goquota.Period) map[string]interface{} {
	data := map[string]interface{}{
		"used":       usage.Used,
		"limit":      usage.Limit,
		"cycleStart": period.Start,
		"tier":       usage.Tier,
		"resource":   usage.Resource,
		"updatedAt":  usage.UpdatedAt,
	}
	if period.Type != goquota.PeriodTypeForever {
		data["cycleEnd"] = period.End
	}
	return data
}

func mergeResultData(req *goquota.StorageMergeRequest, result *goquota.MergeUserResult, now time.Time) map[string]interface{} {
	transfers := make([]map[string]interface{}, 0, len(result.Transfers))
	for _, tr := range result.Transfers {
		transfers = append(transfers, map[string]interface{}{
			"resource":   tr.Resource,
			"periodType": string(tr.PeriodType),
			"amount":     tr.Amount,
		})
	}
	return map[string]interface{}{
		"sourceUserId":   req.SourceUserID,
		"targetUserId":   req.TargetUserID,
		"transfers":      transfers,
		"createdAt":      now,
		"idempotencyKey": req.IdempotencyKey,
	}
}

func mergeResultFromData(data map[string]interface{}) (*goquota.MergeUserResult, error) {
	result := &goquota.MergeUserResult{}
	raw, ok := data["transfers"].([]interface{})
	if !ok {
		return result, nil
	}
	result.Transfers = make([]goquota.MergeTransfer, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		result.Transfers = append(result.Transfers, goquota.MergeTransfer{
			Resource:   getString(m, "resource"),
			PeriodType: goquota.PeriodType(getString(m, "periodType")),
			Amount:     getInt(m, "amount"),
		})
	}
	return result, nil
}
