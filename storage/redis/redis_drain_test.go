package redis

import (
	"context"
	"testing"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestStorage_DoesNotImplementUserMerger(t *testing.T) {
	var store *Storage
	if _, ok := any(store).(goquota.UserMerger); ok {
		t.Fatal("redis storage must not implement UserMerger")
	}
	if _, ok := any(store).(goquota.RemainingDrainer); !ok {
		t.Fatal("redis storage must implement RemainingDrainer")
	}
}

func TestDrainRemaining(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	store, err := New(client, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	period := goquota.Period{Type: goquota.PeriodTypeForever}
	if err := store.SetUsage(ctx, "u1", "scans", &goquota.Usage{
		Used: 2, Limit: 6, Period: period,
	}, period); err != nil {
		t.Fatal(err)
	}
	if err := store.DrainRemaining(ctx, "u1", "scans", period); err != nil {
		t.Fatal(err)
	}
	usage, err := store.GetUsage(ctx, "u1", "scans", period)
	if err != nil || usage == nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Used != 2 || usage.Limit != 2 {
		t.Fatalf("got used=%d limit=%d", usage.Used, usage.Limit)
	}
}
