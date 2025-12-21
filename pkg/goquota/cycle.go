package goquota

import "time"

// currentCycleForStart calculates the current billing cycle for a given subscription start date.
// It preserves the anniversary day-of-month across months, handling month-end edge cases.
//
// For example, if a subscription started on Jan 31, the cycles will be:
//   - Jan 31 - Feb 28 (or Feb 29 in leap years)
//   - Feb 28 - Mar 31
//   - Mar 31 - Apr 30
//   - etc.
func currentCycleForStart(start, now time.Time) (cycleStart, cycleEnd time.Time) {
s := startOfDayUTC(start.UTC())
n := now.UTC()
if n.Before(s) {
// Clock skew / future start: clamp.
end := addMonthsSafe(s, 1)
return s, end
}

// Track the original day-of-month to preserve billing anniversary
originalDay := s.Day()
monthsElapsed := 0

for {
// Calculate cycle start by adding months to original start date
cycleStart = addMonthsSafeWithDay(s, monthsElapsed, originalDay)
cycleEnd = addMonthsSafeWithDay(s, monthsElapsed+1, originalDay)

if cycleEnd.After(n) {
return cycleStart, cycleEnd
}
monthsElapsed++
}
}

// addMonthsSafeWithDay adds months while preserving the target day-of-month when possible.
// If the target day doesn't exist in the result month (e.g., Feb 31), it uses the last day of that month.
func addMonthsSafeWithDay(base time.Time, months, targetDay int) time.Time {
year, month, _ := base.Date()
targetDate := time.Date(year, month+time.Month(months), 1, base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())

// Find the last day of the target month
lastDay := time.Date(targetDate.Year(), targetDate.Month()+1, 0, 0, 0, 0, 0, targetDate.Location()).Day()

actualDay := targetDay
if actualDay > lastDay {
actualDay = lastDay
}

return time.Date(targetDate.Year(), targetDate.Month(), actualDay, base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
}

// addMonthsSafe adds months to a time, handling month-end edge cases.
// Standard Go pattern: Use time.Date with day=1 to avoid overflow, then clip to max day.
func addMonthsSafe(t time.Time, months int) time.Time {
year, month, day := t.Date()
targetDate := time.Date(year, month+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())

// Find the last day of the target month.
// day=0 of month+1 is the last day of month.
lastDay := time.Date(targetDate.Year(), targetDate.Month()+1, 0, 0, 0, 0, 0, targetDate.Location()).Day()

actualDay := day
if actualDay > lastDay {
actualDay = lastDay
}

return time.Date(targetDate.Year(), targetDate.Month(), actualDay, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// startOfDayUTC returns the start of day (00:00:00) in UTC for the given time.
func startOfDayUTC(t time.Time) time.Time {
tt := t.UTC()
return time.Date(tt.Year(), tt.Month(), tt.Day(), 0, 0, 0, 0, time.UTC)
}
