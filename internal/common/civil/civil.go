// Package civil holds helpers for civil (calendar) dates. A civil date is a
// time.Time at UTC midnight; local time zones are never used.
package civil

import "time"

const Layout = "2006-01-02"

// New returns the civil date y-m-d at UTC midnight.
func New(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Parse parses a YYYY-MM-DD string into a civil date.
func Parse(s string) (time.Time, error) {
	return time.ParseInLocation(Layout, s, time.UTC)
}

// Format renders a civil date as YYYY-MM-DD.
func Format(t time.Time) string {
	return t.UTC().Format(Layout)
}

// Truncate drops the time-of-day, returning the UTC civil date of t.
func Truncate(t time.Time) time.Time {
	u := t.UTC()
	return New(u.Year(), u.Month(), u.Day())
}

// Today returns today's civil date in UTC.
func Today() time.Time {
	return Truncate(time.Now())
}

// DaysInMonth returns the number of days in the given month.
func DaysInMonth(y int, m time.Month) int {
	return New(y, m+1, 0).Day()
}

// AddDays returns t shifted by n days.
func AddDays(t time.Time, n int) time.Time {
	return t.AddDate(0, 0, n)
}

// MonthDayClamped returns the date in (y, m) with the given day, clamped to
// the month length (31st -> 30th/28th, Feb 29 -> Feb 28 in non-leap years).
// m may be out of 1..12; it is normalised.
func MonthDayClamped(y int, m int, day int) time.Time {
	first := New(y, time.Month(m), 1)
	dim := DaysInMonth(first.Year(), first.Month())
	if day > dim {
		day = dim
	}
	return New(first.Year(), first.Month(), day)
}

// Ordinal returns the n-th weekday-of-month index of t (1 for the first such
// weekday in the month, 2 for the second, ...).
func Ordinal(t time.Time) int {
	return (t.Day()-1)/7 + 1
}

// NthWeekday returns the n-th (1..5) given weekday of (y, m); ok is false when
// the month has no such date (e.g. a 5th Friday).
func NthWeekday(y int, m time.Month, wd time.Weekday, n int) (time.Time, bool) {
	first := New(y, m, 1)
	offset := (int(wd) - int(first.Weekday()) + 7) % 7
	day := 1 + offset + (n-1)*7
	if day > DaysInMonth(y, m) {
		return time.Time{}, false
	}
	return New(y, m, day), true
}
