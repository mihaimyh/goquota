package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestMergeUser_Emulator(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	store, err := New(client, Config{
		EntitlementsCollection: "test_entitlements_merge",
		UsageCollection:        "test_usage_merge",
		MergeRecordsCollection: "test_merge_records",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	period := goquota.Period{Type: goquota.PeriodTypeForever, Start: now}
	if err := store.SetUsage(ctx, "anon", "scans", &goquota.Usage{Used: 1, Limit: 4, Period: period}, period); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUsage(ctx, "auth", "scans", &goquota.Usage{Used: 0, Limit: 2, Period: period}, period); err != nil {
		t.Fatal(err)
	}

	req := &goquota.StorageMergeRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		IdempotencyKey: "fs-merge-1",
		SealSource:     true,
		Now:            now,
		Items: []goquota.MergeItem{{
			Resource:     "scans",
			PeriodType:   goquota.PeriodTypeForever,
			SourcePeriod: period,
			TargetPeriod: period,
		}},
	}
	result, err := store.MergeUser(ctx, req)
	if err != nil {
		t.Fatalf("MergeUser: %v", err)
	}
	if result.IdempotentReplay {
		t.Fatal("first merge replayed")
	}

	replay, err := store.MergeUser(ctx, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("second merge must replay")
	}

	auth, err := store.GetUsage(ctx, "auth", "scans", period)
	if err != nil || auth == nil {
		t.Fatalf("auth usage: %v", err)
	}
	if auth.Limit != 5 || auth.Used != 0 {
		t.Fatalf("auth forever=%+v", auth)
	}

	ent, err := store.GetEntitlement(ctx, "anon")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !ent.IsSealed(now.Add(time.Minute)) {
		t.Fatalf("expected sealed source: %+v", ent)
	}
}
