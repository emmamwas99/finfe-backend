package calendar

import (
	"testing"
	"time"
)

func TestMonthBasics(t *testing.T) {
	m := MustMonth("2028-02")
	if m.Days() != 29 {
		t.Fatalf("leap February: %d", m.Days())
	}
	if MustMonth("2027-02").Days() != 28 {
		t.Fatal("non-leap February")
	}
	from, to := MustMonth("2026-12").Range()
	if from.Format(time.DateOnly) != "2026-12-01" || to.Format(time.DateOnly) != "2027-01-01" {
		t.Fatalf("range %v %v", from, to)
	}
	if MustMonth("2026-11").Add(3).String() != "2027-02" || MustMonth("2026-01").Add(-1).String() != "2025-12" {
		t.Fatal("add")
	}
	if MonthsBetween(MustMonth("2026-11"), MustMonth("2028-08")) != 21 {
		t.Fatal("between")
	}
	if _, err := ParseMonth("2026-13"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDayInMonthClamps(t *testing.T) {
	if d := DayInMonth(MustMonth("2027-02"), 31); d.Day() != 28 {
		t.Fatalf("got %v", d)
	}
	if d := DayInMonth(MustMonth("2028-02"), 30); d.Day() != 29 {
		t.Fatalf("got %v", d)
	}
}

func TestTodayUsesTimezone(t *testing.T) {
	// 22:30 UTC on 31 Dec is already 1 Jan in Nairobi (UTC+3)
	now := time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC)
	if got := Today(now, "Africa/Nairobi").Format(time.DateOnly); got != "2027-01-01" {
		t.Fatalf("got %s", got)
	}
	if got := Today(now, "America/New_York").Format(time.DateOnly); got != "2026-12-31" {
		t.Fatalf("got %s", got)
	}
	if got := Today(now, "Not/AZone").Format(time.DateOnly); got != "2026-12-31" {
		t.Fatalf("invalid zone falls back to UTC, got %s", got)
	}
}
