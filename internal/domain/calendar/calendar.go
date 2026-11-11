// Package calendar handles months and date ranges. Months are dates pinned to the 1st;
// ranges are half-open [From, To) so month boundaries and leap years need no special cases.
package calendar

import (
	"fmt"
	"time"
)

// Month is the first day of a calendar month, at midnight UTC.
type Month struct{ t time.Time }

// MonthOf returns the month containing d (using d's own year/month/day fields).
func MonthOf(d time.Time) Month {
	return Month{time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)}
}

// ParseMonth parses "YYYY-MM".
func ParseMonth(s string) (Month, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Month{}, fmt.Errorf("month must be YYYY-MM")
	}
	return Month{t}, nil
}

// MustMonth is ParseMonth for constants in tests and code.
func MustMonth(s string) Month {
	m, err := ParseMonth(s)
	if err != nil {
		panic(err)
	}
	return m
}

// Today returns the current calendar date in the given IANA timezone, as a UTC midnight date.
func Today(now time.Time, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

func (m Month) Time() time.Time     { return m.t }
func (m Month) String() string      { return m.t.Format("2006-01") }
func (m Month) Add(n int) Month     { return Month{m.t.AddDate(0, n, 0)} }
func (m Month) Next() Month         { return m.Add(1) }
func (m Month) Before(o Month) bool { return m.t.Before(o.t) }
func (m Month) After(o Month) bool  { return m.t.After(o.t) }
func (m Month) Equal(o Month) bool  { return m.t.Equal(o.t) }
func (m Month) IsZero() bool        { return m.t.IsZero() }
func (m Month) Number() int         { return int(m.t.Month()) }

// Days is the number of days in the month (handles leap years).
func (m Month) Days() int { return m.t.AddDate(0, 1, -1).Day() }

// Range is [first day of month, first day of next month).
func (m Month) Range() (time.Time, time.Time) { return m.t, m.t.AddDate(0, 1, 0) }

// Label is "November 2026".
func (m Month) Label() string { return m.t.Format("January 2006") }

// MonthsBetween counts whole months from a to b (b − a).
func MonthsBetween(a, b Month) int {
	return (b.t.Year()-a.t.Year())*12 + int(b.t.Month()) - int(a.t.Month())
}

// DateOnly truncates a time to its calendar date (UTC midnight).
func DateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ParseDate parses "YYYY-MM-DD".
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date must be YYYY-MM-DD")
	}
	return t, nil
}

// DayInMonth returns the given day clamped to the month length (31 → 28/29 in February).
func DayInMonth(m Month, day int) time.Time {
	if day < 1 {
		day = 1
	}
	if d := m.Days(); day > d {
		day = d
	}
	return m.t.AddDate(0, 0, day-1)
}
