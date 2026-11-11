// Package database opens the PostgreSQL pool, runs migrations and wraps transactions.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"finfe/internal/infrastructure/persistence/store"
	"finfe/migrations"
)

// Open creates a connection pool and verifies connectivity.
func Open(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// Migrate applies (up) or reverts one step (down) of the embedded migrations.
func Migrate(pool *pgxpool.Pool, direction string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	drv, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		return err
	}
	switch direction {
	case "up":
		err = m.Up()
	case "down":
		err = m.Steps(-1)
	default:
		return fmt.Errorf("unknown migration direction %q", direction)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

// InTx runs fn in a transaction with sqlc queries bound to it. Rolls back on error or panic.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *store.Queries) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(store.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsNoRows reports a "not found" query result.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Constraint returns the violated constraint's SQLSTATE and name, if err is a Postgres error.
func Constraint(err error) (code, name string, ok bool) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code, pg.ConstraintName, true
	}
	return "", "", false
}

// SQLSTATE codes we translate into friendly errors.
const (
	UniqueViolation     = "23505"
	ForeignKeyViolation = "23503"
	CheckViolation      = "23514"
)
