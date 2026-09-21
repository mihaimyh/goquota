package goquota

import "time"

// CurrentCycleForStart calculates the current billing cycle for a subscription
// that started at 'start', relative to 'now'. It preserves the day-of-month
// from the start date across months (anniversary-based billing).
//
// IMPORTANT: This function prevents "date drift" by always snapping back to the
// original anchor day when possible. For example, if a subscription started on
// Jan 31, the cycles will be:
//   - Jan 31 - Feb 28 (or Feb 29 in leap years) - Feb has no 31st, so uses last day
//   - Feb 28 - Mar 31 - SNAPS BACK to 31st (not drifting to 28th)
//   - Mar 31 - Apr 30 - Apr has no 31st, so uses last day
//   - Apr 30 - May 31 - SNAPS BACK to 31st (not drifting to 30th)
//   - etc.
//
// This ensures that users don't lose days over time due to month-end variations.
// The anchor day (31st in this example) is preserved whenever the target month
// has that day available.
func CurrentCycleForStart(start, now time.Time) (cycleStart, cycleEnd time.Time) {
	s := startOfDayUTC(start.UTC())
	n := now.UTC()
	if n.Before(s) {
		// Clock skew / future start: clamp.
		end := addMonthsSafe(s, 1)
		return s, end
	}

	// Track the original day-of-month to preserve billing anniversary.
	// The month offset is computed arithmetically (with a small adjustment)
	// instead of scanning month by month, so this stays O(1) even for very old
	// or zero-value start dates.
	originalDay := s.Day()
	monthsElapsed := (n.Year()-s.Year())*12 + int(n.Month()) - int(s.Month())

	// Snap to the greatest cycle start not after now. addMonthsSafeWithDay can
	// land past `now` when the anniversary day is later in the month than n's
	// day, so step back (at most once).
	cycleStart = addMonthsSafeWithDay(s, monthsElapsed, originalDay)
	for cycleStart.After(n) {
		monthsElapsed--
		cycleStart = addMonthsSafeWithDay(s, monthsElapsed, originalDay)
	}

	// Cycle is [cycleStart, cycleEnd) where cycleEnd is exclusive.
	cycleEnd = addMonthsSafeWithDay(s, monthsElapsed+1, originalDay)
	return cycleStart, cycleEnd
}

// addMonthsSafeWithDay adds months while preserving the target day-of-month when possible.
// This function implements the "snap-back" behavior for anniversary billing:
// - If the target day exists in the result month, it uses that day (preserves anchor day)
// - If the target day doesn't exist (e.g., Feb 31), it uses the last day of that month
// - On the next month, it snaps back to the original target day if available
//
// This prevents date drift: Jan 31 -> Feb 28 -> Mar 31 (snaps back), not Mar 28 (drift).
func addMonthsSafeWithDay(base time.Time, months, targetDay int) time.Time {
	year, month, _ := base.Date()
	targetDate := time.Date(year, month+time.Month(months), 1,
		base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())

	// Find the last day of the target month
	lastDay := time.Date(targetDate.Year(), targetDate.Month()+1, 0, 0, 0, 0, 0, targetDate.Location()).Day()

	actualDay := targetDay
	if actualDay > lastDay {
		actualDay = lastDay
	}

	return time.Date(targetDate.Year(), targetDate.Month(), actualDay,
		base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
}

// addMonthsSafe adds months to a time, handling month-end edge cases.
// Standard Go pattern: Use time.Date with day=1 to avoid overflow, then clip to max day.
func addMonthsSafe(t time.Time, months int) time.Time {
	year, month, day := t.Date()
	targetDate := time.Date(year, month+time.Month(months), 1,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())

	// Find the last day of the target month.
	// day=0 of month+1 is the last day of month.
	lastDay := time.Date(targetDate.Year(), targetDate.Month()+1, 0, 0, 0, 0, 0, targetDate.Location()).Day()

	actualDay := day
	if actualDay > lastDay {
		actualDay = lastDay
	}

	return time.Date(targetDate.Year(), targetDate.Month(), actualDay,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// startOfDayUTC returns the start of day (00:00:00) in UTC for the given time.
func startOfDayUTC(t time.Time) time.Time {
	tt := t.UTC()
	return time.Date(tt.Year(), tt.Month(), tt.Day(), 0, 0, 0, 0, time.UTC)
}
