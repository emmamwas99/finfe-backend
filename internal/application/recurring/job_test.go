package recurring

import (
	"testing"
	"time"

	"finfe/internal/domain/calendar"
)

func TestDueClampsMonthlyDayAndHonorsBounds(t *testing.T) {
	day := int16(31)
	r := rule{Frequency: "monthly", DayOfMonth: &day, Start: calendar.MustMonth("2024-01").Time()}
	if !due(r, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("leap-day occurrence missing")
	}
	if due(r, time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("posted early")
	}
	end := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	r.End = &end
	if due(r, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("posted after end")
	}
}
