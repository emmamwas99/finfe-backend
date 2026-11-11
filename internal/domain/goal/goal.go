// Package goal computes progress towards a financial goal.
package goal

import (
	"time"

	"finfe/internal/domain/calendar"
	"finfe/internal/domain/money"
)

type Status string

const (
	Completed  Status = "completed"
	OnTrack    Status = "on_track"
	Behind     Status = "behind"
	Overdue    Status = "overdue"
	NoDeadline Status = "no_deadline"
)

// Progress towards a target.
type Progress struct {
	SavedMinor         int64
	RemainingMinor     int64
	Fraction           float64
	MonthsLeft         int   // 0 when there is no deadline or it has passed
	MonthlyNeededMinor int64 // to finish by the deadline (0 if none)
	Status             Status
}

// Compute progress. "On track" compares saved with a straight line from the goal's start
// month to its target month; it's a simple, explainable yardstick, not a forecast.
func Compute(savedMinor, targetMinor int64, created time.Time, target *calendar.Month, today time.Time) Progress {
	p := Progress{SavedMinor: savedMinor, RemainingMinor: max(0, targetMinor-savedMinor), Fraction: min(1, money.Ratio(savedMinor, targetMinor))}
	if savedMinor >= targetMinor {
		p.Status = Completed
		return p
	}
	if target == nil {
		p.Status = NoDeadline
		return p
	}
	now := calendar.MonthOf(today)
	left := calendar.MonthsBetween(now, *target)
	if left < 0 {
		p.Status = Overdue
		return p
	}
	p.MonthsLeft = left + 1 // the current month still counts
	p.MonthlyNeededMinor = (p.RemainingMinor + int64(p.MonthsLeft) - 1) / int64(p.MonthsLeft)

	start := calendar.MonthOf(created)
	span := calendar.MonthsBetween(start, *target) + 1
	elapsed := calendar.MonthsBetween(start, now) + 1
	if span <= 0 {
		span = 1
	}
	expected := targetMinor * int64(min(elapsed, span)) / int64(span)
	if savedMinor >= expected {
		p.Status = OnTrack
	} else {
		p.Status = Behind
	}
	return p
}
