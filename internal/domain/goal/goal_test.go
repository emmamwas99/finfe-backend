package goal

import (
	"testing"
	"time"

	"finfe/internal/domain/calendar"
)

func d(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

func TestCompute(t *testing.T) {
	target := calendar.MustMonth("2027-10") // 12 months from Nov 2026 inclusive
	created := d("2026-11-01")
	p := Compute(10_000_00, 120_000_00, created, &target, d("2026-11-20"))
	if p.MonthsLeft != 12 || p.MonthlyNeededMinor != 9_166_67 || p.Status != OnTrack {
		t.Fatalf("%+v", p)
	}
	behind := Compute(10_000_00, 120_000_00, created, &target, d("2027-04-10"))
	if behind.Status != Behind {
		t.Fatalf("%+v", behind)
	}
	if Compute(120_000_00, 120_000_00, created, &target, d("2027-04-10")).Status != Completed {
		t.Fatal("completed")
	}
	if Compute(1, 100, created, &target, d("2027-11-01")).Status != Overdue {
		t.Fatal("overdue")
	}
	if Compute(1, 100, created, nil, d("2027-11-01")).Status != NoDeadline {
		t.Fatal("no deadline")
	}
}
