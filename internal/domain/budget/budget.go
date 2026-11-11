// Package budget computes budget periods and progress.
package budget

import (
	"time"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/money"
)

type Period string

const (
	Monthly Period = "monthly"
	Weekly  Period = "weekly"
)

// Level of a budget's utilisation.
type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelOver Level = "over"
)

// Range returns the half-open period [from, to) containing day. Weeks start on Monday (ISO 8601).
func Range(p Period, day time.Time) (time.Time, time.Time) {
	day = calendar.DateOnly(day)
	if p == Weekly {
		offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
		from := day.AddDate(0, 0, -offset)
		return from, from.AddDate(0, 0, 7)
	}
	return calendar.MonthOf(day).Range()
}

// Progress of a budget in its current period.
type Progress struct {
	From, To       time.Time
	SpentMinor     int64
	RemainingMinor int64 // negative when over
	Utilization    float64
	Level          Level
	DaysLeft       int
}

// Compute builds the progress. Warn at thresholdBp of the amount (default 80%), over when spent > amount.
func Compute(amountMinor, spentMinor, thresholdBp int64, from, to, today time.Time) Progress {
	p := Progress{From: from, To: to, SpentMinor: spentMinor, RemainingMinor: amountMinor - spentMinor, Utilization: money.Ratio(spentMinor, amountMinor)}
	switch {
	case spentMinor > amountMinor:
		p.Level = LevelOver
	case spentMinor >= money.MulBp(amountMinor, thresholdBp):
		p.Level = LevelWarn
	default:
		p.Level = LevelOK
	}
	if t := calendar.DateOnly(today); !t.Before(from) && t.Before(to) {
		p.DaysLeft = int(to.Sub(t).Hours() / 24)
	}
	return p
}
