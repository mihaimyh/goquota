package goquota

import (
	"errors"
	"testing"
	"time"
)

func TestEntitlementIsSealed(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name string
		ent  *Entitlement
		want bool
	}{
		{name: "nil", want: false},
		{name: "not sealed", ent: &Entitlement{}, want: false},
		{name: "sealed no expiry", ent: &Entitlement{Sealed: true}, want: true},
		{name: "sealed future expiry", ent: &Entitlement{Sealed: true, ExpireAt: &future}, want: true},
		{name: "expired tombstone", ent: &Entitlement{Sealed: true, ExpireAt: &past}, want: false},
		{name: "expires exactly now", ent: &Entitlement{Sealed: true, ExpireAt: &now}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ent.IsSealed(now); got != tt.want {
				t.Fatalf("IsSealed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemainingCredits(t *testing.T) {
	tests := []struct {
		limit, used, want int
	}{
		{limit: 10, used: 3, want: 7},
		{limit: 5, used: 5, want: 0},
		{limit: 5, used: 8, want: 0},
		{limit: -1, used: 2, want: 0},
		{limit: 0, used: 0, want: 0},
	}
	for _, tt := range tests {
		if got := remainingCredits(tt.limit, tt.used); got != tt.want {
			t.Fatalf("remainingCredits(%d,%d)=%d want %d", tt.limit, tt.used, got, tt.want)
		}
	}
}

func TestApplyMergePair_DailyAddsUsedAndZerosSource(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	srcPeriod := Period{Type: PeriodTypeDaily, Start: now}
	dstPeriod := srcPeriod
	src := &Usage{Used: 4, Limit: 10, Tier: "free"}
	dst := &Usage{Used: 2, Limit: 20, Tier: "pro"}

	got := ApplyMergePair(PeriodTypeDaily, src, dst, srcPeriod, dstPeriod, now)
	if !got.WriteSrc || !got.WriteDst {
		t.Fatalf("expected writes, got src=%v dst=%v", got.WriteSrc, got.WriteDst)
	}
	if got.Transferred != 4 {
		t.Fatalf("transferred=%d want 4", got.Transferred)
	}
	if got.Src.Used != 0 {
		t.Fatalf("source used=%d want 0", got.Src.Used)
	}
	if got.Src.Limit != 10 {
		t.Fatalf("source limit changed: %d", got.Src.Limit)
	}
	if got.Dst.Used != 6 {
		t.Fatalf("dest used=%d want 6", got.Dst.Used)
	}
	if got.Dst.Limit != 20 {
		t.Fatalf("dest limit=%d want 20 (limits must not be added)", got.Dst.Limit)
	}
}

func TestApplyMergePair_ForeverMovesRemainingOnly(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	srcPeriod := Period{Type: PeriodTypeForever}
	dstPeriod := srcPeriod
	src := &Usage{Used: 3, Limit: 10, Tier: "free"}
	dst := &Usage{Used: 1, Limit: 4, Tier: "pro"}

	got := ApplyMergePair(PeriodTypeForever, src, dst, srcPeriod, dstPeriod, now)
	if got.Transferred != 7 {
		t.Fatalf("transferred=%d want 7 remaining", got.Transferred)
	}
	if got.Src.Used != 3 {
		t.Fatalf("source used must stay 3, got %d", got.Src.Used)
	}
	if got.Src.Limit != 3 {
		t.Fatalf("source limit=%d want 3 (drained)", got.Src.Limit)
	}
	if got.Dst.Used != 1 {
		t.Fatalf("dest used must stay 1, got %d", got.Dst.Used)
	}
	if got.Dst.Limit != 11 {
		t.Fatalf("dest limit=%d want 11", got.Dst.Limit)
	}
}

func TestApplyMergePair_ForeverZeroRemainingDrainsSource(t *testing.T) {
	now := time.Now().UTC()
	period := Period{Type: PeriodTypeForever}
	src := &Usage{Used: 5, Limit: 5}
	got := ApplyMergePair(PeriodTypeForever, src, nil, period, period, now)
	if got.Transferred != 0 {
		t.Fatalf("transferred=%d want 0", got.Transferred)
	}
	if !got.WriteSrc {
		t.Fatal("expected source drain write")
	}
	if got.WriteDst {
		t.Fatal("must not create dest when remaining is 0")
	}
	if got.Src.Limit != 5 {
		t.Fatalf("source limit=%d want 5", got.Src.Limit)
	}
}

func TestApplyMergePair_ForeverNilDestCreatesRemainingOnly(t *testing.T) {
	now := time.Now().UTC()
	period := Period{Type: PeriodTypeForever}
	src := &Usage{Used: 2, Limit: 5, Tier: "free"}
	got := ApplyMergePair(PeriodTypeForever, src, nil, period, period, now)
	if got.Transferred != 3 || !got.WriteDst {
		t.Fatalf("want dest created with remaining 3, got %+v", got)
	}
	if got.Dst.Used != 0 {
		t.Fatalf("new dest used=%d want 0", got.Dst.Used)
	}
	if got.Dst.Limit != 3 {
		t.Fatalf("new dest limit=%d want 3", got.Dst.Limit)
	}
}

func TestApplyMergePair_UnlimitedForeverSkipped(t *testing.T) {
	now := time.Now().UTC()
	period := Period{Type: PeriodTypeForever}
	src := &Usage{Used: 2, Limit: -1}
	got := ApplyMergePair(PeriodTypeForever, src, &Usage{Limit: 4}, period, period, now)
	if got.WriteSrc || got.WriteDst || got.Transferred != 0 {
		t.Fatalf("unlimited forever must be skipped, got %+v", got)
	}
}

func TestApplyMergePair_NilSourceNoop(t *testing.T) {
	now := time.Now().UTC()
	period := Period{Type: PeriodTypeDaily}
	got := ApplyMergePair(PeriodTypeDaily, nil, &Usage{Used: 1}, period, period, now)
	if got.WriteSrc || got.WriteDst || got.Transferred != 0 {
		t.Fatalf("nil source must be no-op, got %+v", got)
	}
}

func TestApplyMergePair_MonthlySameAsDaily(t *testing.T) {
	now := time.Now().UTC()
	period := Period{Type: PeriodTypeMonthly}
	src := &Usage{Used: 9, Limit: 100}
	dst := &Usage{Used: 1, Limit: 50}
	got := ApplyMergePair(PeriodTypeMonthly, src, dst, period, period, now)
	if got.Dst.Used != 10 || got.Src.Used != 0 || got.Dst.Limit != 50 {
		t.Fatalf("monthly merge mismatch: %+v", got)
	}
}

func TestValidateMergeUserRequest(t *testing.T) {
	valid := &MergeUserRequest{
		SourceUserID:   "anon",
		TargetUserID:   "auth",
		Resources:      []string{"scans"},
		Periods:        []PeriodType{PeriodTypeForever},
		IdempotencyKey: "k1",
	}

	if err := validateMergeUserRequest(valid); err != nil {
		t.Fatalf("valid request failed: %v", err)
	}

	same := *valid
	same.TargetUserID = "anon"
	if err := validateMergeUserRequest(&same); !errors.Is(err, ErrSameUser) {
		t.Fatalf("want ErrSameUser, got %v", err)
	}

	auto := *valid
	auto.Periods = []PeriodType{PeriodTypeAuto}
	if err := validateMergeUserRequest(&auto); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("want ErrInvalidPeriod, got %v", err)
	}

	empty := *valid
	empty.Resources = nil
	if err := validateMergeUserRequest(&empty); !errors.Is(err, ErrInvalidMergeRequest) {
		t.Fatalf("want ErrInvalidMergeRequest, got %v", err)
	}
}

func TestDrainLimit(t *testing.T) {
	if drainLimit(4) != 4 {
		t.Fatal("drainLimit(4)")
	}
	if drainLimit(-2) != 0 {
		t.Fatal("drainLimit clamps negative used")
	}
}

func TestSealExpireAtDefault(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := sealExpireAt(now, 0)
	want := now.Add(DefaultIdentitySealTTL)
	if !got.Equal(want) {
		t.Fatalf("default TTL expire=%v want %v", got, want)
	}
}
