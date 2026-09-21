package goquota_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// warnRecorder captures Warn messages so tests can assert that a lossy
// operation was surfaced rather than failing silently.
type warnRecorder struct {
	mu      sync.Mutex
	entries []string
}

func (w *warnRecorder) Debug(string, ...goquota.Field) {}
func (w *warnRecorder) Info(string, ...goquota.Field)  {}
func (w *warnRecorder) Error(string, ...goquota.Field) {}

func (w *warnRecorder) Warn(msg string, _ ...goquota.Field) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, msg)
}

func (w *warnRecorder) warns() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.entries...)
}

func promotionMergeManager(t *testing.T) (*goquota.Manager, *memory.Storage, *warnRecorder) {
	t.Helper()
	store := memory.New()
	rec := &warnRecorder{}
	cfg := goquota.Config{
		DefaultTier: "explorer",
		Logger:      rec,
		Tiers: map[string]goquota.TierConfig{
			"explorer": {Name: "explorer", DailyQuotas: map[string]int{"scans": 5}},
			"premium":  {Name: "premium", DailyQuotas: map[string]int{"scans": 500}},
		},
	}
	mgr, err := goquota.NewManager(store, &cfg)
	require.NoError(t, err)
	return mgr, store, rec
}

func TestManagerMergeUser_ReportsDroppedPromotion(t *testing.T) {
	mgr, store, rec := promotionMergeManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	daily := goquota.Period{Start: dayStart, End: dayStart.Add(24 * time.Hour), Type: goquota.PeriodTypeDaily}

	mustSetUsage(t, ctx, store, "anon", "scans", daily, 3, 5)
	mustSetUsage(t, ctx, store, "auth", "scans", daily, 1, 5)

	require.NoError(t, store.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "anon", Tier: "explorer", SubscriptionStartDate: dayStart, UpdatedAt: now,
	}))
	require.NoError(t, store.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "auth", Tier: "explorer", SubscriptionStartDate: dayStart, UpdatedAt: now,
	}))

	_, err := mgr.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID: "anon", Tier: "premium", ExpiresAt: now.Add(time.Hour), Source: "incident_2026",
	})
	require.NoError(t, err)

	result, err := mgr.MergeUser(ctx, &goquota.MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeDaily},
		IdempotencyKey: "merge-with-promotion",
	})
	require.NoError(t, err)

	require.NotNil(t, result.DroppedPromotion, "the source promotion must not be dropped silently")
	assert.Equal(t, "premium", result.DroppedPromotion.Tier)
	assert.Equal(t, "incident_2026", result.DroppedPromotion.Source)

	// The merge must not move the promotion onto the target.
	targetTier, err := mgr.GetEffectiveTier(ctx, "auth")
	require.NoError(t, err)
	assert.Equal(t, "explorer", targetTier, "merge must not widen the target's access")

	assert.NotEmpty(t, rec.warns(), "dropping a promotion must be logged")
}

func TestManagerMergeUser_NoPromotionNoDrop(t *testing.T) {
	mgr, store, rec := promotionMergeManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	daily := goquota.Period{Start: dayStart, End: dayStart.Add(24 * time.Hour), Type: goquota.PeriodTypeDaily}

	mustSetUsage(t, ctx, store, "anon", "scans", daily, 2, 5)
	mustSetUsage(t, ctx, store, "auth", "scans", daily, 0, 5)

	result, err := mgr.MergeUser(ctx, &goquota.MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []goquota.PeriodType{goquota.PeriodTypeDaily},
		IdempotencyKey: "merge-without-promotion",
	})
	require.NoError(t, err)
	assert.Nil(t, result.DroppedPromotion)
	assert.Empty(t, rec.warns())
}
