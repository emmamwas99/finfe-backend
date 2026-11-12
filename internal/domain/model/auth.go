package model

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID                  uuid.UUID
	Email               string
	PasswordHash        string
	Name                string
	EmailVerifiedAt     *time.Time
	Currency            string
	Country             string
	Timezone            string
	Language            string
	ExpectedIncomeMinor int64
	WarnAtBp            int32
	AutoLockMinutes     int32
	LastBackupAt        *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type UserSession struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	UserAgent  string
	Ip         string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

type IncomeExpectation struct {
	UserID      uuid.UUID
	Scope       string
	Month       time.Time
	AmountMinor int64
}

type CreateEmailVerificationParams struct {
	TokenHash []byte
	UserID    uuid.UUID
	ExpiresAt time.Time
}

type CreatePasswordResetParams struct {
	TokenHash []byte
	UserID    uuid.UUID
	ExpiresAt time.Time
}

type CreateRefreshTokenParams struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
}

type CreateSessionParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	UserAgent string
	Ip        string
	ExpiresAt time.Time
}

type CreateSubscriptionParams struct {
	UserID      uuid.UUID
	TrialEndsAt *time.Time
}

type CreateUserParams struct {
	ID                  uuid.UUID
	Email               string
	PasswordHash        string
	Name                string
	Currency            string
	Country             string
	Timezone            string
	Language            string
	ExpectedIncomeMinor int64
}

type GetRefreshTokenRow struct {
	ID               uuid.UUID
	SessionID        uuid.UUID
	ExpiresAt        time.Time
	UsedAt           *time.Time
	UserID           uuid.UUID
	SessionRevokedAt *time.Time
	SessionExpiresAt time.Time
}

type InsertAuditEventParams struct {
	UserID    *uuid.UUID
	Action    string
	Ip        string
	UserAgent string
	Metadata  []byte
}

type RevokeSessionParams struct {
	ID     uuid.UUID
	UserID uuid.UUID
}

type SetUserPasswordParams struct {
	PasswordHash string
	ID           uuid.UUID
}

type TouchSessionParams struct {
	ExpiresAt time.Time
	ID        uuid.UUID
}

type UpdateUserParams struct {
	Name                string
	Currency            string
	Country             string
	Timezone            string
	Language            string
	ExpectedIncomeMinor int64
	WarnAtBp            int32
	AutoLockMinutes     int32
	LastBackupAt        *time.Time
	ID                  uuid.UUID
}
