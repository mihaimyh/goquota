package goquota

import (
	"errors"
	"strings"
	"time"
)

// ErrInvalidTimezone is returned when a timezone string is not a valid IANA name.
var ErrInvalidTimezone = errors.New("invalid IANA timezone")

// NormalizeIANATimezone validates and normalizes an IANA timezone name.
// Empty input resolves to UTC.
func NormalizeIANATimezone(timezone string) (string, bool) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return "UTC", true
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", false
	}
	return timezone, true
}

// LoadLocationOrUTC loads an IANA timezone or returns UTC on empty/invalid input.
func LoadLocationOrUTC(timezone string) *time.Location {
	if normalized, ok := NormalizeIANATimezone(timezone); ok {
		loc, err := time.LoadLocation(normalized)
		if err == nil {
			return loc
		}
	}
	return time.UTC
}

// DailyPeriodBounds returns UTC instants for [local midnight, next local midnight).
func DailyPeriodBounds(now time.Time, timezone string) (start, end time.Time) {
	loc := LoadLocationOrUTC(timezone)
	local := now.In(loc)
	startLocal := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	endLocal := startLocal.AddDate(0, 0, 1)
	return startLocal.UTC(), endLocal.UTC()
}

// DailyPeriod builds a daily quota period using the provided IANA timezone.
// Empty or invalid timezones fall back to UTC boundaries.
func DailyPeriod(now time.Time, timezone string) Period {
	normalized, ok := NormalizeIANATimezone(timezone)
	if !ok {
		normalized = "UTC"
	}
	start, end := DailyPeriodBounds(now, normalized)
	return Period{
		Start:    start,
		End:      end,
		Type:     PeriodTypeDaily,
		Timezone: normalized,
	}
}

func entitlementTimezone(ent *Entitlement) string {
	if ent == nil {
		return ""
	}
	return ent.Timezone
}

// dailyPeriodForEntitlement returns the daily period for a user entitlement.
func dailyPeriodForEntitlement(now time.Time, ent *Entitlement) Period {
	return DailyPeriod(now, entitlementTimezone(ent))
}
