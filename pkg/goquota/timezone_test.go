package goquota

import (
	"testing"
	"time"
)

func TestNormalizeIANATimezone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{name: "valid timezone", input: "Europe/Bucharest", want: "Europe/Bucharest", wantOK: true},
		{name: "trims whitespace", input: "  Europe/Bucharest  ", want: "Europe/Bucharest", wantOK: true},
		{name: "empty uses UTC", input: "", want: "UTC", wantOK: true},
		{name: "invalid timezone", input: "Not/A_Real_Zone", want: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := NormalizeIANATimezone(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDailyPeriodBounds_UTC(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 1, 43, 0, 0, time.UTC)
	start, end := DailyPeriodBounds(now, "UTC")

	wantStart := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)

	if !start.Equal(wantStart) {
		t.Fatalf("start = %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %v, want %v", end, wantEnd)
	}
}

func TestDailyPeriodBounds_EuropeBucharest(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// 2026-09-03 01:43 local → resets at next local midnight (same calendar day end)
	now := time.Date(2026, 9, 3, 1, 43, 0, 0, loc)
	start, end := DailyPeriodBounds(now, "Europe/Bucharest")

	wantStart := time.Date(2026, 9, 3, 0, 0, 0, 0, loc).UTC()
	wantEnd := time.Date(2026, 9, 4, 0, 0, 0, 0, loc).UTC()

	if !start.Equal(wantStart) {
		t.Fatalf("start = %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %v, want %v", end, wantEnd)
	}
}

func TestDailyPeriodBounds_InvalidTimezoneFallsBackToUTC(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	start, end := DailyPeriodBounds(now, "Invalid/Zone")

	wantStart := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)

	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("fallback bounds incorrect: start=%v end=%v", start, end)
	}
}

func TestDailyPeriod_KeyUsesLocalCalendarDate(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	now := time.Date(2026, 9, 3, 23, 30, 0, 0, loc)
	period := DailyPeriod(now, "Europe/Bucharest")

	if got, want := period.Key(), "2026-09-03"; got != want {
		t.Fatalf("period key = %q, want %q", got, want)
	}
}

func TestDailyPeriod_AmericaNewYorkDSTEnd(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// Fall-back Sunday 2026-11-01: 25-hour local day; next midnight still Nov 2 00:00 local.
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, loc)
	start, end := DailyPeriodBounds(now, "America/New_York")

	wantStart := time.Date(2026, 11, 1, 0, 0, 0, 0, loc).UTC()
	wantEnd := time.Date(2026, 11, 2, 0, 0, 0, 0, loc).UTC()

	if !start.Equal(wantStart) {
		t.Fatalf("start = %v, want %v", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %v, want %v", end, wantEnd)
	}
	if duration := end.Sub(start); duration != 25*time.Hour {
		t.Fatalf("DST end day duration = %v, want 25h", duration)
	}
}
