// Package recurring posts due rules at most once per local date. The database
// unique constraint is the final guard when two workers run concurrently.
package recurring

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"finfe/internal/domain/calendar"
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

// Run catches up no more than one year of missed occurrences per rule.
func Run(ctx context.Context, db *pgxpool.Pool, now time.Time) error {
	rows, err := db.Query(ctx, `SELECT r.id,r.user_id,r.kind,r.name,r.amount_minor,r.currency,r.category_id,r.source,r.frequency,r.day_of_month,r.weekday,r.start_date,r.end_date,r.last_posted_on,u.timezone FROM recurring_transactions r JOIN users u ON u.id=r.user_id WHERE r.active`)
	if err != nil {
		return err
	}
	var rules []rule
	for rows.Next() {
		var r rule
		if err = rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.Name, &r.Amount, &r.Currency, &r.CategoryID, &r.Source, &r.Frequency, &r.DayOfMonth, &r.Weekday, &r.Start, &r.End, &r.Last, &r.Timezone); err != nil {
			rows.Close()
			return err
		}
		rules = append(rules, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range rules {
		today := calendar.Today(now, r.Timezone)
		first := r.Start
		if r.Last != nil && !r.Last.Before(first) {
			first = r.Last.AddDate(0, 0, 1)
		}
		if cutoff := today.AddDate(0, 0, -366); first.Before(cutoff) {
			first = cutoff
		}
		for day := calendar.DateOnly(first); !day.After(today); day = day.AddDate(0, 0, 1) {
			if !due(r, day) {
				continue
			}
			if err := post(ctx, db, r, day); err != nil {
				return fmt.Errorf("recurring %s on %s: %w", r.ID, day.Format(time.DateOnly), err)
			}
		}
	}
	return nil
}

func post(ctx context.Context, db *pgxpool.Pool, r rule, day time.Time) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, `SELECT active FROM recurring_transactions WHERE id=$1 FOR UPDATE`, r.ID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	if r.Kind == "expense" {
		if _, err = tx.Exec(ctx, `INSERT INTO expenses(id,user_id,occurred_on,amount_minor,currency,category_id,note,recurring_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (recurring_id,occurred_on) DO NOTHING`, uuid.New(), r.UserID, day, r.Amount, r.Currency, r.CategoryID, r.Name, r.ID); err != nil {
			return err
		}
	} else {
		source := r.Source
		if source == "" {
			source = r.Name
		}
		if _, err = tx.Exec(ctx, `INSERT INTO income(id,user_id,occurred_on,amount_minor,currency,source,category_id,recurring_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (recurring_id,occurred_on) DO NOTHING`, uuid.New(), r.UserID, day, r.Amount, r.Currency, source, r.CategoryID, r.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE recurring_transactions SET last_posted_on=GREATEST(coalesce(last_posted_on,$2::date),$2::date),updated_at=now() WHERE id=$1`, r.ID, day); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
