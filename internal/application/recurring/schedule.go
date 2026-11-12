package recurring

import (
	"finfe/internal/domain/calendar"
	"github.com/google/uuid"
	"time"
)

type rule struct {
	ID, UserID                                        uuid.UUID
	Kind, Name, Currency, Source, Frequency, Timezone string
	CategoryID                                        *uuid.UUID
	Amount                                            int64
	DayOfMonth, Weekday                               *int16
	Start                                             time.Time
	End, Last                                         *time.Time
}

func due(r rule, day time.Time) bool {
	if day.Before(r.Start) || r.End != nil && day.After(*r.End) {
		return false
	}
	if r.Frequency == "weekly" {
		return r.Weekday != nil && int(day.Weekday()) == int(*r.Weekday)
	}
	return r.DayOfMonth != nil && day.Day() == calendar.DayInMonth(calendar.MonthOf(day), int(*r.DayOfMonth)).Day()
}
