// Package devicetoken is the data-access layer for the device_tokens
// table (migration 0009). Per-user Expo push tokens with platform metadata.
// Re-registering the same (user_id, expo_token) pair is an UPDATE-by-pair
// rather than a duplicate row — the UNIQUE index enforces the invariant.
package devicetoken

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
var ErrNotFound = errors.New("devicetoken: not found")

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
INSERT INTO device_tokens (id, user_id, expo_token, platform)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, expo_token) DO UPDATE
SET platform     = EXCLUDED.platform,
    last_seen_at = now()
RETURNING id, user_id, expo_token, platform, created_at, last_seen_at`

const deleteSQL = `-- name: Delete :execrows
DELETE FROM device_tokens
WHERE id = $1 AND user_id = $2`

const listByUserSQL = `-- name: ListByUser :many
SELECT id, user_id, expo_token, platform, created_at, last_seen_at
FROM device_tokens
WHERE user_id = $1
ORDER BY last_seen_at DESC, id DESC`

// scanRow decodes one row into domain.DeviceToken. Centralized so column
// order is consistent across queries.
func scanRow(row pgx.Row) (domain.DeviceToken, error) {
	var d domain.DeviceToken
	err := row.Scan(
		&d.ID,
		&d.UserID,
		&d.ExpoToken,
		&d.Platform,
		&d.CreatedAt,
		&d.LastSeenAt,
	)
	return d, err
}

// Register inserts a new device token row, or updates platform +
// last_seen_at if (userID, expoToken) already exists. The returned row's
// id is the persistent identifier — callers that need to delete a token
// later use this id, not the expo token itself.
func (q *Queries) Register(ctx context.Context, userID uuid.UUID, expoToken string, platform domain.DevicePlatform) (domain.DeviceToken, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return domain.DeviceToken{}, fmt.Errorf("devicetoken: register: generate id: %w", err)
	}
	tok, err := scanRow(q.db.QueryRow(ctx, registerSQL, id, userID, expoToken, string(platform)))
	if err != nil {
		return domain.DeviceToken{}, fmt.Errorf("devicetoken: register: %w", err)
	}
	return tok, nil
}

// Delete removes the row identified by (id, userID). The userID scope
// prevents one user from deleting another user's token. Returns ErrNotFound
// if no row matched.
func (q *Queries) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := q.db.Exec(ctx, deleteSQL, id, userID)
	if err != nil {
		return fmt.Errorf("devicetoken: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByUser returns every device_tokens row for userID, newest-first by
// last_seen_at. Returns an empty slice (not nil) when the user has no
// registered devices, so callers can range without a nil check.
func (q *Queries) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeviceToken, error) {
	rows, err := q.db.Query(ctx, listByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("devicetoken: list by user: %w", err)
	}
	defer rows.Close()

	out := make([]domain.DeviceToken, 0)
	for rows.Next() {
		tok, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("devicetoken: list scan: %w", err)
		}
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("devicetoken: list rows: %w", err)
	}
	return out, nil
}
