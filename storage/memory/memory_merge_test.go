package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestDrainRemaining_Idempotent(t *testing.T) {
	store := New()
	ctx := context.Background()
	period := goquota.Period{Type: goquota.PeriodTypeForever, Start: time.Now().UTC()}
	err := store.SetUsage(ctx, "u1", "scans", &goquota.Usage{Used: 3, Limit: 9, Period: period}, period)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DrainRemaining(ctx, "u1", "scans", period); err != nil {
		t.Fatal(err)
	}
	if err := store.DrainRemaining(ctx, "u1", "scans", period); err != nil {
		t.Fatal(err)
	}
	usage, err := store.GetUsage(ctx, "u1", "scans", period)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Used != 3 || usage.Limit != 3 {
		t.Fatalf("got used=%d limit=%d", usage.Used, usage.Limit)
	}
}

func TestMergeUser_ConcurrentIdempotent(t *testing.T) {
	store := New()
	ctx := context.Background()
	period := goquota.Period{Type: goquota.PeriodTypeDaily, Start: time.Now().UTC()}
	_ = store.SetUsage(ctx, "src", "scans", &goquota.Usage{Used: 4, Limit: 10, Period: period}, period)
	_ = store.SetUsage(ctx, "dst", "scans", &goquota.Usage{Used: 1, Limit: 10, Period: period}, period)

	req := &goquota.StorageMergeRequest{
		SourceUserID:   "src",
		TargetUserID:   "dst",
		IdempotencyKey: "merge-concurrent",
		Now:            time.Now().UTC(),
		Items: []goquota.MergeItem{{
			Resource:     "scans",
			PeriodType:   goquota.PeriodTypeDaily,
			SourcePeriod: period,
			TargetPeriod: period,
		}},
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.MergeUser(ctx, req)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("MergeUser: %v", err)
		}
	}

	dst, err := store.GetUsage(ctx, "dst", "scans", period)
	if err != nil {
		t.Fatal(err)
	}
	if dst.Used != 5 {
		t.Fatalf("concurrent merge doubled used: %d", dst.Used)
	}
}
