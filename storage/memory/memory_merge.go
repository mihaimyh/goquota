package memory

import (
	"context"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

var (
	_ goquota.RemainingDrainer = (*Storage)(nil)
	_ goquota.UserMerger       = (*Storage)(nil)
)

// DrainRemaining implements goquota.RemainingDrainer.
func (s *Storage) DrainRemaining(_ context.Context, userID, resource string, period goquota.Period) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := usageKey(userID, resource, period)
	usage, ok := s.usage[key]
	if !ok {
		return nil
	}
	usage.Limit = usage.Used
	if usage.Limit < 0 {
		usage.Limit = 0
	}
	usage.UpdatedAt = time.Now().UTC()
	return nil
}

// MergeUser implements goquota.UserMerger under the process mutex.
func (s *Storage) MergeUser(_ context.Context, req *goquota.StorageMergeRequest) (*goquota.MergeUserResult, error) {
	if req == nil || req.IdempotencyKey == "" {
		return nil, goquota.ErrInvalidMergeRequest
	}
	if req.SourceUserID == req.TargetUserID {
		return nil, goquota.ErrSameUser
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.mergeRecords[req.IdempotencyKey]; ok {
		replay := cloneMergeResult(existing)
		replay.IdempotentReplay = true
		return replay, nil
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if s.entitlements[req.SourceUserID].IsSealed(now) || s.entitlements[req.TargetUserID].IsSealed(now) {
		return nil, goquota.ErrUserSealed
	}

	result := &goquota.MergeUserResult{
		Transfers: make([]goquota.MergeTransfer, 0, len(req.Items)),
	}
	for _, item := range req.Items {
		srcKey := usageKey(req.SourceUserID, item.Resource, item.SourcePeriod)
		dstKey := usageKey(req.TargetUserID, item.Resource, item.TargetPeriod)

		var src, dst *goquota.Usage
		if existing, ok := s.usage[srcKey]; ok {
			copySrc := *existing
			src = &copySrc
		}
		if existing, ok := s.usage[dstKey]; ok {
			copyDst := *existing
			dst = &copyDst
		}

		pair := goquota.ApplyMergePair(item.PeriodType, src, dst, item.SourcePeriod, item.TargetPeriod, now)
		if pair.WriteSrc {
			pair.Src.UserID = req.SourceUserID
			pair.Src.Resource = item.Resource
			copied := pair.Src
			s.usage[srcKey] = &copied
		}
		if pair.WriteDst {
			pair.Dst.UserID = req.TargetUserID
			pair.Dst.Resource = item.Resource
			copied := pair.Dst
			s.usage[dstKey] = &copied
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
		s.sealUser(req.SourceUserID, req.TargetUserID, req.SealExpireAt, now)
	}

	stored := cloneMergeResult(result)
	s.mergeRecords[req.IdempotencyKey] = stored
	return cloneMergeResult(stored), nil
}

func (s *Storage) sealUser(sourceUserID, targetUserID string, expireAt *time.Time, now time.Time) {
	ent, ok := s.entitlements[sourceUserID]
	if !ok {
		ent = &goquota.Entitlement{UserID: sourceUserID}
		s.entitlements[sourceUserID] = ent
	}
	ent.Sealed = true
	ent.MigratedTo = targetUserID
	ent.ExpireAt = expireAt
	ent.UpdatedAt = now
}

func cloneMergeResult(in *goquota.MergeUserResult) *goquota.MergeUserResult {
	if in == nil {
		return &goquota.MergeUserResult{}
	}
	out := *in
	if in.Transfers != nil {
		out.Transfers = make([]goquota.MergeTransfer, len(in.Transfers))
		copy(out.Transfers, in.Transfers)
	}
	return &out
}
