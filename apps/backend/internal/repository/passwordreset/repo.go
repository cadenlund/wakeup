// Package passwordreset persists the short-lived tokens emailed to users
// during the password-reset flow (table from migration 0008). The auth
// service (§16 milestone 3.2) hashes the user-facing token via SHA-256
// before passing it here, so the DB never stores the plain token.
package passwordreset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is returned when Get can't find a live, unconsumed row.
// Auth treats "wrong token", "expired token", and "already used token"
// uniformly via this sentinel — clients learn nothing about which case
// applied, defeating timing-based oracles.
var ErrNotFound = errors.New("passwordreset: not found")

// Entry mirrors a row in password_resets.
type Entry struct {
	TokenHash []byte
	UserID    uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// Queries is the per-aggregate repository.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :exec
INSERT INTO password_resets (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)`

const getSQL = `-- name: Get :one
SELECT token_hash, user_id, expires_at, used_at
FROM password_resets
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now()`

const markUsedSQL = `-- name: MarkUsed :exec
UPDATE password_resets SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL`

const deleteExpiredAndUsedSQL = `-- name: DeleteExpiredAndUsed :execrows
DELETE FROM password_resets
WHERE expires_at <= now() OR used_at IS NOT NULL`

// Create inserts a new password-reset row. tokenHash is sha256(token) —
// callers must hash before passing here.
func (q *Queries) Create(ctx context.Context, tokenHash []byte, userID uuid.UUID, expiresAt time.Time) error {
	if len(tokenHash) == 0 {
		return errors.New("passwordreset: Create: tokenHash is empty")
	}
	if _, err := q.db.Exec(ctx, createSQL, tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("passwordreset: create: %w", err)
	}
	return nil
}

// Get returns the entry only if the row exists, has not expired, and has
// not been consumed. Returns ErrNotFound otherwise — callers can't
// distinguish between the three failure modes from the error alone, which
// is intentional.
func (q *Queries) Get(ctx context.Context, tokenHash []byte) (Entry, error) {
	row := q.db.QueryRow(ctx, getSQL, tokenHash)
	var e Entry
	err := row.Scan(&e.TokenHash, &e.UserID, &e.ExpiresAt, &e.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("passwordreset: get: %w", err)
	}
	return e, nil
}

// MarkUsed sets used_at = now() on the row. Returns ErrNotFound if the
// row was already consumed (or doesn't exist) — protects against
// double-spend in the unlikely race between two concurrent ConfirmReset
// calls with the same token.
func (q *Queries) MarkUsed(ctx context.Context, tokenHash []byte) error {
	tag, err := q.db.Exec(ctx, markUsedSQL, tokenHash)
	if err != nil {
		return fmt.Errorf("passwordreset: mark used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteExpiredAndUsed garbage-collects rows past their expiry or already
// consumed. Returns the count for the §4.12 sweeper's log line.
func (q *Queries) DeleteExpiredAndUsed(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, deleteExpiredAndUsedSQL)
	if err != nil {
		return 0, fmt.Errorf("passwordreset: delete expired/used: %w", err)
	}
	return tag.RowsAffected(), nil
}
