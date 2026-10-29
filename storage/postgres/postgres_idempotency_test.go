//go:build integration
// +build integration

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestStorage_ConsumeQuota_ExpiredIdempotencyRecordIsNotReplayed covers PGC-1:
// the idempotency lookup ignored expires_at, so a key reused after its TTL was
// replayed instead of charging again.
func TestStorage_ConsumeQuota_ExpiredIdempotencyRecordIsNotReplayed(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	period := goquota.Period{Start: now, End: now.Add(24 * time.Hour), Type: goquota.PeriodTypeDaily}
	req := &goquota.ConsumeRequest{
		UserID:         "user-pgc1",
		Resource:       "api_calls",
		Amount:         1,
		Tier:           "free",
		Period:         period,
		Limit:          10,
		IdempotencyKey: "key-pgc1",
	}

	newUsed, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if newUsed != 1 {
		t.Fatalf("first consume newUsed = %d, want 1", newUsed)
	}

	// Within the TTL the same key must replay.
	replay, err := storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("replay within TTL: %v", err)
	}
	if replay != 1 {
		t.Fatalf("replay newUsed = %d, want 1", replay)
	}

	// Force the record past its TTL.
	if _, err := storage.pool.Exec(ctx,
		`UPDATE consumption_records SET expires_at = NOW() - INTERVAL '1 minute' WHERE consumption_id = $1`,
		"key-pgc1"); err != nil {
		t.Fatalf("expire record: %v", err)
	}

	if record, err := storage.GetConsumptionRecord(ctx, "key-pgc1"); err != nil {
		t.Fatalf("get expired record: %v", err)
	} else if record != nil {
		t.Fatalf("expired record must not be returned, got %+v", record)
	}

	// After the TTL the same key is a new operation and must charge again.
	newUsed, err = storage.ConsumeQuota(ctx, req)
	if err != nil {
		t.Fatalf("consume after TTL: %v", err)
	}
	if newUsed != 2 {
		t.Fatalf("consume after TTL newUsed = %d, want 2", newUsed)
	}

	usage, err := storage.GetUsage(ctx, req.UserID, req.Resource, period)
	if err != nil || usage == nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Used != 2 {
		t.Fatalf("usage.Used = %d, want 2", usage.Used)
	}
}

// TestStorage_RefundQuota_ExpiredIdempotencyRecordIsNotReplayed covers the
// refund side of PGC-1.
func TestStorage_RefundQuota_ExpiredIdempotencyRecordIsNotReplayed(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	period := goquota.Period{Start: now, End: now.Add(24 * time.Hour), Type: goquota.PeriodTypeDaily}
	if err := storage.SetUsage(ctx, "user-refund", "api_calls",
		&goquota.Usage{Used: 5, Limit: 10, Period: period}, period); err != nil {
		t.Fatalf("set usage: %v", err)
	}

	req := &goquota.RefundRequest{
		UserID:         "user-refund",
		Resource:       "api_calls",
		Amount:         2,
		Period:         period,
		IdempotencyKey: "refund-pgc1",
	}
	if err := storage.RefundQuota(ctx, req); err != nil {
		t.Fatalf("first refund: %v", err)
	}

	// Replay within the TTL must not refund twice.
	if err := storage.RefundQuota(ctx, req); err != nil {
		t.Fatalf("replay within TTL: %v", err)
	}
	usage, err := storage.GetUsage(ctx, req.UserID, req.Resource, period)
	if err != nil || usage == nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Used != 3 {
		t.Fatalf("after replay usage.Used = %d, want 3", usage.Used)
	}

	if _, err := storage.pool.Exec(ctx,
		`UPDATE refund_records SET expires_at = NOW() - INTERVAL '1 minute' WHERE refund_id = $1`,
		"refund-pgc1"); err != nil {
		t.Fatalf("expire record: %v", err)
	}
	if record, err := storage.GetRefundRecord(ctx, "refund-pgc1"); err != nil {
		t.Fatalf("get expired record: %v", err)
	} else if record != nil {
		t.Fatalf("expired refund record must not be returned, got %+v", record)
	}

	// After the TTL the key is a new operation, so the refund applies again.
	if err := storage.RefundQuota(ctx, req); err != nil {
		t.Fatalf("refund after TTL: %v", err)
	}
	usage, err = storage.GetUsage(ctx, req.UserID, req.Resource, period)
	if err != nil || usage == nil {
		t.Fatalf("usage after TTL: %v", err)
	}
	if usage.Used != 1 {
		t.Fatalf("after TTL usage.Used = %d, want 1", usage.Used)
	}
}

// TestStorage_MergeUser_RejectsKeyReuseForDifferentPair covers PGM-1: a replay
// returned the stored result for any caller, so the same key reused for another
// target reported success and merged nothing.
func TestStorage_MergeUser_RejectsKeyReuseForDifferentPair(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	period := goquota.Period{
		Start: now,
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}
	if err := storage.SetUsage(ctx, "src", "scans", &goquota.Usage{Used: 1, Limit: 4, Period: period}, period); err != nil {
		t.Fatalf("set src usage: %v", err)
	}
	if err := storage.SetUsage(ctx, "dst", "scans", &goquota.Usage{Used: 0, Limit: 2, Period: period}, period); err != nil {
		t.Fatalf("set dst usage: %v", err)
	}
	if err := storage.SetUsage(ctx, "other", "scans", &goquota.Usage{Used: 0, Limit: 1, Period: period}, period); err != nil {
		t.Fatalf("set other usage: %v", err)
	}

	mergeReq := func(target string) *goquota.StorageMergeRequest {
		return &goquota.StorageMergeRequest{
			SourceUserID:   "src",
			TargetUserID:   target,
			IdempotencyKey: "merge-pgm1",
			Now:            now,
			Items: []goquota.MergeItem{{
				Resource:     "scans",
				PeriodType:   goquota.PeriodTypeForever,
				SourcePeriod: period,
				TargetPeriod: period,
			}},
		}
	}

	if _, err := storage.MergeUser(ctx, mergeReq("dst")); err != nil {
		t.Fatalf("first merge: %v", err)
	}

	_, err := storage.MergeUser(ctx, mergeReq("other"))
	if err == nil {
		t.Fatal("expected an error when reusing a merge key for a different pair")
	}
	if !strings.Contains(err.Error(), "was used for") {
		t.Fatalf("error should name the original pair, got %v", err)
	}

	other, err := storage.GetUsage(ctx, "other", "scans", period)
	if err != nil || other == nil {
		t.Fatalf("other usage: %v", err)
	}
	if other.Limit != 1 || other.Used != 0 {
		t.Fatalf("other must not gain from the rejected merge: %+v", other)
	}
}
