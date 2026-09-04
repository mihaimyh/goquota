package goquota

import (
	"context"
	"time"
)

// DefaultIdentitySealTTL is the default lifetime of a MergeUser source tombstone.
const DefaultIdentitySealTTL = 30 * 24 * time.Hour

// RemainingDrainer is an optional Storage capability: set Limit = Used without
// changing Used. Drain is single-document and naturally idempotent.
type RemainingDrainer interface {
	DrainRemaining(ctx context.Context, userID, resource string, period Period) error
}

// UserMerger is an optional Storage capability for atomic cross-identity merge.
// Only backends that can commit multiple documents/rows in one transaction
// should implement this (memory, Firestore, Postgres). Redis and tiered storage
// must not implement it; Manager returns ErrUnsupportedOperation instead.
type UserMerger interface {
	MergeUser(ctx context.Context, req *StorageMergeRequest) (*MergeUserResult, error)
}

// MergeUserRequest is the Manager-level merge request.
type MergeUserRequest struct {
	SourceUserID   string
	TargetUserID   string
	Resources      []string     // required, non-empty
	Periods        []PeriodType // required; daily, monthly, and/or forever (not auto)
	IdempotencyKey string       // required; durable, not a 24h consume TTL
	SealSource     bool
	// SealTTL is the tombstone lifetime. Zero uses DefaultIdentitySealTTL.
	SealTTL time.Duration
}

// MergeTransfer is one resource/period movement applied during MergeUser.
type MergeTransfer struct {
	Resource   string
	PeriodType PeriodType
	// Amount is source Used added onto the target for daily/monthly,
	// or remaining credits (Limit-Used) moved for forever.
	Amount int
}

// MergeUserResult is returned by MergeUser.
type MergeUserResult struct {
	IdempotentReplay bool
	Transfers        []MergeTransfer
}

// MergeItem is one resource pair the storage merge applies atomically.
type MergeItem struct {
	Resource     string
	PeriodType   PeriodType
	SourcePeriod Period
	TargetPeriod Period
}

// StorageMergeRequest is the storage-level merge request with resolved periods.
type StorageMergeRequest struct {
	SourceUserID   string
	TargetUserID   string
	Items          []MergeItem
	IdempotencyKey string
	SealSource     bool
	SealExpireAt   *time.Time
	Now            time.Time
}

// MergePairResult is the ledger outcome of merging one source usage into one dest usage.
type MergePairResult struct {
	Src         Usage
	Dst         Usage
	Transferred int
	WriteSrc    bool
	WriteDst    bool
}

func remainingCredits(limit, used int) int {
	if limit < 0 {
		return 0
	}
	rem := limit - used
	if rem < 0 {
		return 0
	}
	return rem
}

func drainLimit(used int) int {
	if used < 0 {
		return 0
	}
	return used
}

// ApplyMergePair computes source/dest usage after a merge. src nil is a no-op.
// Daily/monthly: dest.Used += src.Used; src.Used = 0; limits are not added.
// Forever: dest.Limit += remaining; src.Limit = src.Used; spent Used is not copied.
// Unlimited forever (Limit < 0) is skipped so we do not invent a finite remaining.
func ApplyMergePair(periodType PeriodType, src, dst *Usage, srcPeriod, dstPeriod Period, now time.Time) MergePairResult {
	if src == nil {
		return MergePairResult{}
	}

	srcOut := *src
	srcOut.Period = srcPeriod
	srcOut.UpdatedAt = now

	var dstOut Usage
	if dst != nil {
		dstOut = *dst
	} else {
		dstOut = Usage{
			Period: dstPeriod,
			Tier:   src.Tier,
		}
	}
	dstOut.Period = dstPeriod
	dstOut.UpdatedAt = now

	switch periodType {
	case PeriodTypeForever:
		if src.Limit < 0 {
			return MergePairResult{}
		}
		rem := remainingCredits(src.Limit, src.Used)
		dstOut.Limit += rem
		srcOut.Limit = src.Used
		return MergePairResult{
			Src:         srcOut,
			Dst:         dstOut,
			Transferred: rem,
			WriteSrc:    true,
			WriteDst:    rem > 0 || dst != nil,
		}
	case PeriodTypeDaily, PeriodTypeMonthly:
		moved := src.Used
		if moved < 0 {
			moved = 0
		}
		dstOut.Used += moved
		srcOut.Used = 0
		return MergePairResult{
			Src:         srcOut,
			Dst:         dstOut,
			Transferred: moved,
			WriteSrc:    true,
			WriteDst:    moved > 0 || dst != nil,
		}
	default:
		return MergePairResult{}
	}
}

func validateMergeUserRequest(req *MergeUserRequest) error {
	if req == nil {
		return ErrInvalidMergeRequest
	}
	if req.SourceUserID == "" || req.TargetUserID == "" {
		return ErrInvalidMergeRequest
	}
	if req.SourceUserID == req.TargetUserID {
		return ErrSameUser
	}
	if req.IdempotencyKey == "" {
		return ErrInvalidMergeRequest
	}
	if len(req.Resources) == 0 {
		return ErrInvalidMergeRequest
	}
	for _, resource := range req.Resources {
		if resource == "" {
			return ErrInvalidMergeRequest
		}
	}
	if len(req.Periods) == 0 {
		return ErrInvalidMergeRequest
	}
	for _, periodType := range req.Periods {
		switch periodType {
		case PeriodTypeDaily, PeriodTypeMonthly, PeriodTypeForever:
		default:
			return ErrInvalidPeriod
		}
	}
	return nil
}

func sealExpireAt(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		ttl = DefaultIdentitySealTTL
	}
	return now.Add(ttl)
}
