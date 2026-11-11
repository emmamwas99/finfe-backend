package budget

import (
	"testing"
	"time"
)

func d(s string) time.Time { t, _ := time.Parse(time.DateOnly, s); return t }

func TestRange(t *testing.T) {
	from, to := Range(Monthly, d("2028-02-15"))
	if from != d("2028-02-01") || to != d("2028-03-01") {
		t.Fatalf("monthly %v %v", from, to)
	}
	// Sunday 15 Nov 2026 belongs to the week starting Monday 9 Nov
	from, to = Range(Weekly, d("2026-11-15"))
	if from != d("2026-11-09") || to != d("2026-11-16") {
		t.Fatalf("weekly %v %v", from, to)
	}
	from, _ = Range(Weekly, d("2026-11-16"))
	if from != d("2026-11-16") {
		t.Fatal("Monday starts its own week")
	}
}

func TestCompute(t *testing.T) {
	from, to := Range(Monthly, d("2026-11-15"))
	p := Compute(15_000_00, 12_000_00, 8000, from, to, d("2026-11-15"))
	if p.Level != LevelWarn || p.RemainingMinor != 3_000_00 || p.Utilization != 0.8 || p.DaysLeft != 16 {
		t.Fatalf("%+v", p)
	}
	if Compute(15_000_00, 15_000_01, 8000, from, to, d("2026-11-15")).Level != LevelOver {
		t.Fatal("over")
	}
	if Compute(15_000_00, 15_000_00, 8000, from, to, d("2026-11-15")).Level != LevelWarn {
		t.Fatal("exactly at the limit is a warning, not over")
	}
	if Compute(15_000_00, 1_000_00, 8000, from, to, d("2026-12-01")).DaysLeft != 0 {
		t.Fatal("no days left outside the period")
	}
}
