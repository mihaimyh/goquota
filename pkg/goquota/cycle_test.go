package goquota

import (
	"testing"
	"time"
)

func TestCurrentCycleForStart(t *testing.T) {
	// Anniversary on the 10th
	start := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "In first month",
			now:       time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Exactly on boundary",
			now:       time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 3, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Crosses year boundary",
			now:       time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 12, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Future start (skew)",
			now:       time.Date(2022, 12, 21, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotS, gotE := CurrentCycleForStart(start, tt.now)
			if !gotS.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", gotS, tt.wantStart)
			}
			if !gotE.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", gotE, tt.wantEnd)
			}
		})
	}
}

func TestCurrentCycleForStart_MonthEnd(t *testing.T) {
	// Anniversary on the 31st
	start := time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "February (28 days)",
			now:       time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "March from February",
			now:       time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Leap year (Feb 2024)",
			now:       time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotS, gotE := CurrentCycleForStart(start, tt.now)
			if !gotS.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", gotS, tt.wantStart)
			}
			if !gotE.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", gotE, tt.wantEnd)
			}
		})
	}
}

func TestAddMonthsSafe(t *testing.T) {
	tests := []struct {
		name   string
		base   time.Time
		months int
		want   time.Time
	}{
		{
			name:   "Jan 31 + 1 month = Feb 28",
			base:   time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
			months: 1,
			want:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "Jan 31 + 1 month (Leap) = Feb 29",
			base:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			months: 1,
			want:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "Mar 31 + 1 month = Apr 30",
			base:   time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
			months: 1,
			want:   time.Date(2023, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "Aug 31 + 6 months = Feb 28",
			base:   time.Date(2022, 8, 31, 0, 0, 0, 0, time.UTC),
			months: 6,
			want:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addMonthsSafe(tt.base, tt.months)
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Phase 4.1: Cycle Calculation Edge Cases

func TestCurrentCycleForStart_LeapYear(t *testing.T) {
	// Subscription starts on Feb 29, 2024 (leap year)
	start := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "Same leap year",
			now:       time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 3, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Next year (non-leap)",
			now:       time.Date(2025, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2025, 1, 29, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Next leap year",
			now:       time.Date(2028, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2028, 1, 29, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := CurrentCycleForStart(start, tt.now)
			if !gotStart.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", gotStart, tt.wantStart)
			}
			if !gotEnd.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", gotEnd, tt.wantEnd)
			}
		})
	}
}

func TestCurrentCycleForStart_LeapYearNextYear(t *testing.T) {
	// Subscription starts on Feb 29, 2024 (leap year)
	start := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)

	// Test in 2025 (non-leap year) - should use Feb 28
	now := time.Date(2025, 2, 15, 0, 0, 0, 0, time.UTC)
	gotStart, gotEnd := CurrentCycleForStart(start, now)

	// Should use Feb 28 in non-leap year
	wantStart := time.Date(2025, 1, 29, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC)

	if !gotStart.Equal(wantStart) {
		t.Errorf("start: got %v, want %v", gotStart, wantStart)
	}
	if !gotEnd.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v", gotEnd, wantEnd)
	}
}

func TestCurrentCycleForStart_MonthEndVariations(t *testing.T) {
	tests := []struct {
		name      string
		start     time.Time
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "Jan 31 -> Feb 28",
			start:     time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
			now:       time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Jan 31 -> Feb 29 (leap year)",
			start:     time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			now:       time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Mar 31 -> Apr 30",
			start:     time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
			now:       time.Date(2023, 4, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Aug 31 -> Sep 30",
			start:     time.Date(2023, 8, 31, 0, 0, 0, 0, time.UTC),
			now:       time.Date(2023, 9, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 8, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Dec 31 -> Jan 31 (year boundary)",
			start:     time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			now:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := CurrentCycleForStart(tt.start, tt.now)
			if !gotStart.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", gotStart, tt.wantStart)
			}
			if !gotEnd.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", gotEnd, tt.wantEnd)
			}
		})
	}
}

func TestCurrentCycleForStart_ClockSkew(t *testing.T) {
	// Start date in the future (clock skew scenario)
	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	now := time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC) // Before start

	gotStart, gotEnd := CurrentCycleForStart(start, now)

	// Should return first cycle
	wantStart := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)

	if !gotStart.Equal(wantStart) {
		t.Errorf("start: got %v, want %v", gotStart, wantStart)
	}
	if !gotEnd.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v", gotEnd, wantEnd)
	}
}

func TestCurrentCycleForStart_ExactBoundary(t *testing.T) {
	// Subscription starts on the 10th
	start := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "Exactly at cycle start",
			now:       time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 3, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "One second before cycle start",
			now:       time.Date(2023, 2, 9, 23, 59, 59, 0, time.UTC),
			wantStart: time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "One second after cycle start",
			now:       time.Date(2023, 2, 10, 0, 0, 1, 0, time.UTC),
			wantStart: time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2023, 3, 10, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := CurrentCycleForStart(start, tt.now)
			if !gotStart.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", gotStart, tt.wantStart)
			}
			if !gotEnd.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", gotEnd, tt.wantEnd)
			}
		})
	}
}

func TestCurrentCycleForStart_TimezoneIndependence(t *testing.T) {
	// Test that UTC calculations are consistent regardless of local timezone
	start := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)

	// Test with different timezone times (all should convert to same UTC)
	tests := []struct {
		name string
		now  time.Time
	}{
		{
			name: "UTC time",
			now:  time.Date(2023, 2, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "EST time (UTC-5)",
			now:  time.Date(2023, 2, 15, 7, 0, 0, 0, time.FixedZone("EST", -5*3600)),
		},
		{
			name: "PST time (UTC-8)",
			now:  time.Date(2023, 2, 15, 4, 0, 0, 0, time.FixedZone("PST", -8*3600)),
		},
	}

	// All should produce the same UTC cycle
	// Feb 15 12:00:00 UTC is after Feb 15 00:00:00, so it's in the cycle [Feb 15, Mar 15)
	wantStart := time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := CurrentCycleForStart(start, tt.now)
			if !gotStart.Equal(wantStart) {
				t.Errorf("start: got %v, want %v", gotStart, wantStart)
			}
			if !gotEnd.Equal(wantEnd) {
				t.Errorf("end: got %v, want %v", gotEnd, wantEnd)
			}
		})
	}
}

// TestCurrentCycleForStart_Jan31DriftPrevention explicitly tests the audit scenario:
// Subscription starts Jan 31 -> Feb cycle ends Feb 28 -> March cycle should snap back to March 31
// (NOT drift to March 28). This verifies that anniversary-based billing preserves the anchor day
// and prevents date drift.
func TestCurrentCycleForStart_Jan31DriftPrevention(t *testing.T) {
	// Subscription starts on Jan 31, 2023
	start := time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		now         time.Time
		wantStart   time.Time
		wantEnd     time.Time
		description string
	}{
		{
			name:        "First cycle: Jan 31 - Feb 28",
			now:         time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC),
			wantStart:   time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
			description: "First cycle should end on Feb 28 (last day of February)",
		},
		{
			name:        "Second cycle: Feb 28 - March 31 (SNAP-BACK, not drift)",
			now:         time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC),
			wantStart:   time.Date(2023, 2, 28, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
			description: "CRITICAL: March cycle must end on March 31 (snap-back to anchor day), NOT March 28 (drift)",
		},
		{
			name:        "Third cycle: March 31 - April 30 (continues anchor day preservation)",
			now:         time.Date(2023, 4, 15, 0, 0, 0, 0, time.UTC),
			wantStart:   time.Date(2023, 3, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2023, 4, 30, 0, 0, 0, 0, time.UTC),
			description: "April cycle should end on April 30 (last day of April), preserving the anchor day pattern",
		},
		{
			name:        "Multiple months: Verify no cumulative drift",
			now:         time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC),
			wantStart:   time.Date(2023, 5, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:     time.Date(2023, 6, 30, 0, 0, 0, 0, time.UTC),
			description: "After multiple months, should still preserve anchor day (31st) pattern, not drift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := CurrentCycleForStart(start, tt.now)
			if !gotStart.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v. %s", gotStart, tt.wantStart, tt.description)
			}
			if !gotEnd.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v. %s", gotEnd, tt.wantEnd, tt.description)
			}

			// Explicit assertion for the critical audit scenario
			if tt.name == "Second cycle: Feb 28 - March 31 (SNAP-BACK, not drift)" {
				if gotEnd.Day() != 31 {
					t.Errorf("CRITICAL DRIFT BUG: March cycle ends on day %d, expected 31. This indicates date drift!", gotEnd.Day())
				}
				if gotEnd.Month() != time.March {
					t.Errorf("CRITICAL: Expected March, got %v", gotEnd.Month())
				}
			}
		})
	}
}
