package firestore

import (
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/stretchr/testify/assert"
)

// The stored period type has to survive the round-trip.
//
// Reading it as "daily, else monthly" silently turned a forever consumption
// (pre-paid credits) into a monthly one. A replay then reported the wrong
// Period, and RefundFromConsume - which resolves PeriodTypeAuto from the
// consumption record - refunded a pool that was never charged.
func TestPeriodTypeFromRecord_PreservesEveryPeriodType(t *testing.T) {
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	foreverEnd := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

	for _, tc := range []struct {
		stored string
		end    time.Time
		want   goquota.PeriodType
	}{
		{"daily", start.Add(24 * time.Hour), goquota.PeriodTypeDaily},
		{"monthly", start.AddDate(0, 1, 0), goquota.PeriodTypeMonthly},
		{"forever", foreverEnd, goquota.PeriodTypeForever},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			assert.Equal(t, tc.want, periodTypeFromRecord(tc.stored, start, tc.end))
		})
	}
}

// Records written before the periodType field existed must not silently claim
// the wrong pool either; the cycle itself says which one it was.
func TestPeriodTypeFromRecord_InfersLegacyRecordsFromTheirCycle(t *testing.T) {
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

	assert.Equal(
		t, goquota.PeriodTypeForever,
		periodTypeFromRecord("", start, time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)),
		"a far-future cycle end is a forever pool",
	)
	assert.Equal(
		t, goquota.PeriodTypeDaily,
		periodTypeFromRecord("", start, start.Add(24*time.Hour)),
	)
	assert.Equal(
		t, goquota.PeriodTypeMonthly,
		periodTypeFromRecord("", start, start.AddDate(0, 1, 0)),
	)
}
