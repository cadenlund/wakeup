// Package notificationpref is the data-access layer for the
// notification_preferences table (migration 0012). Per-user toggles for
// push-notification categories AND the mobile §4.5 theme pick. The row
// is auto-created with schema defaults on first read; from then on the
// service patches individual fields.
package notificationpref

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is returned by Patch when no row matches userID. Callers
// can compare with errors.Is.
var ErrNotFound = errors.New("notificationpref: not found")

// Queries is the per-aggregate repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// PatchParams is the input to Patch. Pointer fields use nil-means-
// unchanged semantics — service layer assembles only the fields the
// caller asked to change.
type PatchParams struct {
	UserID              uuid.UUID
	DirectMessages      *bool
	GroupMessages       *bool
	FriendRequests      *bool
	Calls               *bool
	ThemeScheme         *string
	ThemeModePreference *string
}

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const getOrCreateSQL = `-- name: GetOrCreate :one
INSERT INTO notification_preferences (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING user_id, direct_messages, group_messages, friend_requests, calls,
          theme_scheme, theme_mode_preference, updated_at`

const getSQL = `-- name: Get :one
SELECT user_id, direct_messages, group_messages, friend_requests, calls,
       theme_scheme, theme_mode_preference, updated_at
FROM notification_preferences
WHERE user_id = $1`

const patchSQL = `-- name: Patch :one
UPDATE notification_preferences
SET direct_messages       = COALESCE($2, direct_messages),
    group_messages        = COALESCE($3, group_messages),
    friend_requests       = COALESCE($4, friend_requests),
    calls                 = COALESCE($5, calls),
    theme_scheme          = COALESCE($6, theme_scheme),
    theme_mode_preference = COALESCE($7, theme_mode_preference)
WHERE user_id = $1
RETURNING user_id, direct_messages, group_messages, friend_requests, calls,
          theme_scheme, theme_mode_preference, updated_at`

// scanRow decodes one row into domain.NotificationPreference. Centralized
// so column order is consistent across queries.
func scanRow(row pgx.Row) (domain.NotificationPreference, error) {
	var p domain.NotificationPreference
	err := row.Scan(
		&p.UserID,
		&p.DirectMessages,
		&p.GroupMessages,
		&p.FriendRequests,
		&p.Calls,
		&p.ThemeScheme,
		&p.ThemeModePreference,
		&p.UpdatedAt,
	)
	return p, err
}

// Get returns the user's preference row if it exists, or ErrNotFound
// if the user has never touched their preferences. The §11 ShouldNotify
// gate uses this to avoid forcing a write on every notification trigger.
func (q *Queries) Get(ctx context.Context, userID uuid.UUID) (domain.NotificationPreference, error) {
	pref, err := scanRow(q.db.QueryRow(ctx, getSQL, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationPreference{}, ErrNotFound
	}
	if err != nil {
		return domain.NotificationPreference{}, fmt.Errorf("notificationpref: get: %w", err)
	}
	return pref, nil
}

// GetOrCreate returns the user's preference row, inserting one with
// schema defaults if it doesn't exist yet. Callers can treat this as
// "give me the row, create-if-needed" — it's idempotent and cheap.
func (q *Queries) GetOrCreate(ctx context.Context, userID uuid.UUID) (domain.NotificationPreference, error) {
	pref, err := scanRow(q.db.QueryRow(ctx, getOrCreateSQL, userID))
	if err != nil {
		return domain.NotificationPreference{}, fmt.Errorf("notificationpref: get or create: %w", err)
	}
	return pref, nil
}

// Patch updates whichever fields are non-nil in p, leaving the rest
// untouched. Returns ErrNotFound if no row exists for the user (caller
// should call GetOrCreate first to ensure the row).
func (q *Queries) Patch(ctx context.Context, p PatchParams) (domain.NotificationPreference, error) {
	pref, err := scanRow(q.db.QueryRow(ctx, patchSQL,
		p.UserID,
		p.DirectMessages, p.GroupMessages, p.FriendRequests, p.Calls,
		p.ThemeScheme, p.ThemeModePreference,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationPreference{}, ErrNotFound
	}
	if err != nil {
		return domain.NotificationPreference{}, fmt.Errorf("notificationpref: patch: %w", err)
	}
	return pref, nil
}
