//go:build integration
// +build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestStorage_MergeUser_ForeverAndSeal(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	period := goquota.Period{
		Start: now,
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	if err := storage.SetUsage(ctx, "anon", "scans", &goquota.Usage{Used: 1, Limit: 4, Period: period}, period); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetUsage(ctx, "auth", "scans", &goquota.Usage{Used: 0, Limit: 2, Period: period}, period); err != nil {
		t.Fatal(err)
	}

	expire := now.Add(30 * 24 * time.Hour)
	req := &goquota.StorageMergeRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		IdempotencyKey: "pg-merge-1",
		SealSource:     true,
		SealExpireAt:   &expire,
		Now:            now,
		Items: []goquota.MergeItem{{
			Resource:     "scans",
			PeriodType:   goquota.PeriodTypeForever,
			SourcePeriod: period,
			TargetPeriod: period,
		}},
	}
	result, err := storage.MergeUser(ctx, req)
	if err != nil {
		t.Fatalf("MergeUser: %v", err)
	}
	if result.IdempotentReplay {
		t.Fatal("first merge replayed")
	}

	replay, err := storage.MergeUser(ctx, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("second merge must replay")
	}

	auth, err := storage.GetUsage(ctx, "auth", "scans", period)
	if err != nil || auth == nil {
		t.Fatalf("auth usage: %v", err)
	}
	if auth.Limit != 5 || auth.Used != 0 {
		t.Fatalf("auth forever=%+v", auth)
	}

	ent, err := storage.GetEntitlement(ctx, "anon")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !ent.IsSealed(now.Add(time.Minute)) || ent.MigratedTo != "auth" {
		t.Fatalf("expected sealed source: %+v", ent)
	}
}

func TestStorage_DrainRemaining(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	period := goquota.Period{Start: time.Now().UTC(), Type: goquota.PeriodTypeForever}
	if err := storage.SetUsage(ctx, "u1", "scans", &goquota.Usage{Used: 2, Limit: 7, Period: period}, period); err != nil {
		t.Fatal(err)
	}
	if err := storage.DrainRemaining(ctx, "u1", "scans", period); err != nil {
		t.Fatal(err)
	}
	usage, err := storage.GetUsage(ctx, "u1", "scans", period)
	if err != nil || usage == nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Used != 2 || usage.Limit != 2 {
		t.Fatalf("got used=%d limit=%d", usage.Used, usage.Limit)
	}
}
