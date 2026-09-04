package goquota_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

type storageOnly struct {
	goquota.Storage
}

func testMergeManager(t *testing.T) (*goquota.Manager, *memory.Storage) {
	t.Helper()
	store := memory.New()
	cfg := goquota.Config{
		DefaultTier: "explorer",
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: "explorer",
				DailyQuotas: map[string]int{
					"scans": 5,
				},
				MonthlyQuotas: map[string]int{
					"scans": 50,
				},
			},
		},
	}
	mgr, err := goquota.NewManager(store, &cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr, store
}

func mustSetUsage(t *testing.T, ctx context.Context, store *memory.Storage, userID, resource string, period goquota.Period, used, limit int) {
	t.Helper()
	err := store.SetUsage(ctx, userID, resource, &goquota.Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      used,
		Limit:     limit,
		Period:    period,
		UpdatedAt: time.Now().UTC(),
	}, period)
	if err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
}

func TestManagerDrainRemaining_ForeverKeepsUsed(t *testing.T) {
	mgr, store := testMergeManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	period := goquota.Period{
		Start: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}
	mustSetUsage(t, ctx, store, "u1", "scans", period, 2, 8)

	if err := mgr.DrainRemaining(ctx, "u1", "scans", goquota.PeriodTypeForever); err != nil {
		t.Fatalf("DrainRemaining: %v", err)
	}

	usage, err := store.GetUsage(ctx, "u1", "scans", period)
	if err != nil || usage == nil {
		t.Fatalf("GetUsage: %v usage=%v", err, usage)
	}
	if usage.Used != 2 {
		t.Fatalf("used=%d want 2", usage.Used)
	}
	if usage.Limit != 2 {
		t.Fatalf("limit=%d want 2", usage.Limit)
	}

	if err := mgr.DrainRemaining(ctx, "u1", "scans", goquota.PeriodTypeForever); err != nil {
		t.Fatalf("second DrainRemaining: %v", err)
	}
	usage, _ = store.GetUsage(ctx, "u1", "scans", period)
	if usage.Used != 2 || usage.Limit != 2 {
		t.Fatalf("drain must be idempotent, got used=%d limit=%d", usage.Used, usage.Limit)
	}
}

func TestManagerMergeUser_DailyAndForever(t *testing.T) {
	mgr, store := testMergeManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	daily := goquota.Period{Start: dayStart, End: dayStart.Add(24 * time.Hour), Type: goquota.PeriodTypeDaily}
	forever := goquota.Period{
		Start: dayStart,
		End:   time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		Type:  goquota.PeriodTypeForever,
	}

	mustSetUsage(t, ctx, store, "anon", "scans", daily, 3, 5)
	mustSetUsage(t, ctx, store, "auth", "scans", daily, 1, 5)
	mustSetUsage(t, ctx, store, "anon", "scans", forever, 1, 4)
	mustSetUsage(t, ctx, store, "auth", "scans", forever, 0, 2)

	result, err := mgr.MergeUser(ctx, &goquota.MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeDaily, goquota.PeriodTypeForever},
		IdempotencyKey: "merge-1",
		SealSource:     true,
	})
	if err != nil {
		t.Fatalf("MergeUser: %v", err)
	}
	if result.IdempotentReplay {
		t.Fatal("first merge must not be a replay")
	}

	anonDaily, _ := store.GetUsage(ctx, "anon", "scans", daily)
	authDaily, _ := store.GetUsage(ctx, "auth", "scans", daily)
	if anonDaily.Used != 0 || authDaily.Used != 4 || authDaily.Limit != 5 {
		t.Fatalf("daily merge: anon=%+v auth=%+v", anonDaily, authDaily)
	}

	anonForever, _ := store.GetUsage(ctx, "anon", "scans", forever)
	authForever, _ := store.GetUsage(ctx, "auth", "scans", forever)
	if anonForever.Used != 1 || anonForever.Limit != 1 {
		t.Fatalf("anon forever drain: %+v", anonForever)
	}
	if authForever.Used != 0 || authForever.Limit != 5 {
		t.Fatalf("auth forever remaining: %+v", authForever)
	}

	ent, err := store.GetEntitlement(ctx, "anon")
	if err != nil {
		t.Fatalf("sealed entitlement: %v", err)
	}
	if !ent.IsSealed(time.Now().UTC()) || ent.MigratedTo != "auth" {
		t.Fatalf("expected sealed source, got %+v", ent)
	}

	if _, err := mgr.Consume(ctx, "anon", "scans", 1, goquota.PeriodTypeDaily); !errors.Is(err, goquota.ErrUserSealed) {
		t.Fatalf("consume sealed user: %v", err)
	}

	replay, err := mgr.MergeUser(ctx, &goquota.MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeDaily, goquota.PeriodTypeForever},
		IdempotencyKey: "merge-1",
		SealSource:     true,
	})
	if err != nil {
		t.Fatalf("replay MergeUser: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("second merge must replay")
	}
	authDaily, _ = store.GetUsage(ctx, "auth", "scans", daily)
	if authDaily.Used != 4 {
		t.Fatalf("replay must not double-add used, got %d", authDaily.Used)
	}

	_, err = mgr.MergeUser(ctx, &goquota.MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeForever},
		IdempotencyKey: "merge-2-new-key",
	})
	if !errors.Is(err, goquota.ErrUserSealed) {
		t.Fatalf("new merge against sealed source: %v", err)
	}
}

func TestManagerMergeUser_UnsupportedStorage(t *testing.T) {
	inner := memory.New()
	mgr, err := goquota.NewManager(storageOnly{Storage: inner}, &goquota.Config{
		DefaultTier: "explorer",
		Tiers: map[string]goquota.TierConfig{
			"explorer": {Name: "explorer"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.MergeUser(context.Background(), &goquota.MergeUserRequest{
		SourceUserID:   "a",
		TargetUserID:   "b",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeForever},
		IdempotencyKey: "k",
	})
	if !errors.Is(err, goquota.ErrUnsupportedOperation) {
		t.Fatalf("want ErrUnsupportedOperation, got %v", err)
	}
}

func TestManagerMergeUser_SameUser(t *testing.T) {
	mgr, _ := testMergeManager(t)
	_, err := mgr.MergeUser(context.Background(), &goquota.MergeUserRequest{
		SourceUserID:   "a",
		TargetUserID:   "a",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeForever},
		IdempotencyKey: "k",
	})
	if !errors.Is(err, goquota.ErrSameUser) {
		t.Fatalf("want ErrSameUser, got %v", err)
	}
}

func TestManagerDrainRemaining_MissingUsage(t *testing.T) {
	mgr, _ := testMergeManager(t)
	if err := mgr.DrainRemaining(context.Background(), "missing", "scans", goquota.PeriodTypeForever); err != nil {
		t.Fatalf("drain missing usage: %v", err)
	}
}

func TestSetUsageGetQuota_MonthlyAgreesWithZeroSubscriptionStart(t *testing.T) {
	mgr, _ := testMergeManager(t)
	ctx := context.Background()
	if err := mgr.SetEntitlement(ctx, &goquota.Entitlement{UserID: "u-monthly", Tier: "explorer"}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}
	if err := mgr.SetUsage(ctx, "u-monthly", "scans", goquota.PeriodTypeMonthly, 10); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	usage, err := mgr.GetQuota(ctx, "u-monthly", "scans", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if usage == nil || usage.Used != 10 {
		t.Fatalf("used=%v want 10", usage)
	}
}
