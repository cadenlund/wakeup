// Package voiptoken is the data-access layer for the voip_tokens table
// (migration 0009). Per-user iOS PushKit tokens — separate transport
// from Expo push, used by CallKit / VoIP push to wake the app from a
// killed state for incoming calls (mobile §8.6).
package voiptoken

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is returned by Delete when no row matched the (id, user_id)
// pair. Callers can compare with errors.Is.
var ErrNotFound = errors.New("voiptoken: not found")

// Queries is the per-aggregate repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const registerSQL = `-- name: Register :one
INSERT INTO voip_tokens (id, user_id, voip_token)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, voip_token) DO UPDATE
SET last_seen_at = now()
RETURNING id, user_id, voip_token, created_at, last_seen_at`

const deleteSQL = `-- name: Delete :execrows
DELETE FROM voip_tokens
WHERE id = $1 AND user_id = $2`

const listByUserSQL = `-- name: ListByUser :many
SELECT id, user_id, voip_token, created_at, last_seen_at
FROM voip_tokens
WHERE user_id = $1
ORDER BY created_at DESC`

// Register inserts (or refreshes) the user's VoIP token. Idempotent on
// (user_id, voip_token): re-register bumps last_seen_at without
// creating a duplicate row.
func (q *Queries) Register(ctx context.Context, userID uuid.UUID, voipToken string) (domain.VoIPToken, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return domain.VoIPToken{}, fmt.Errorf("voiptoken: uuid: %w", err)
	}
	row := q.db.QueryRow(ctx, registerSQL, id, userID, voipToken)
	var t domain.VoIPToken
	if err := row.Scan(&t.ID, &t.UserID, &t.VoIPToken, &t.CreatedAt, &t.LastSeenAt); err != nil {
		return domain.VoIPToken{}, fmt.Errorf("voiptoken: register: %w", err)
	}
	return t, nil
}

// Delete removes the (id, userID) row. Returns ErrNotFound when no
// row matches — by design we can't tell the caller whether the row
// belongs to a different user vs doesn't exist (no enumeration leak).
func (q *Queries) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := q.db.Exec(ctx, deleteSQL, id, userID)
	if err != nil {
		return fmt.Errorf("voiptoken: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByUser returns every voip token registered to userID, newest
// first. Used by the mobile settings/devices screen alongside the Expo
// list and (future) by the call-fanout to enumerate VoIP recipients.
func (q *Queries) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.VoIPToken, error) {
	rows, err := q.db.Query(ctx, listByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("voiptoken: list by user: %w", err)
	}
	defer rows.Close()
	var out []domain.VoIPToken
	for rows.Next() {
		var t domain.VoIPToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.VoIPToken, &t.CreatedAt, &t.LastSeenAt); err != nil {
			return nil, fmt.Errorf("voiptoken: scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("voiptoken: rows: %w", err)
	}
	return out, nil
}
