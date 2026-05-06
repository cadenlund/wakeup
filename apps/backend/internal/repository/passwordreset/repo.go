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

// ErrNotFound is returned when Get can't find a row with the given
// token hash, or when the row exists but has already been consumed
// (used_at is set). Used and missing are treated identically — clients
// learn nothing from the difference, defeating timing oracles and
// preventing an attacker from confirming a reused token was once valid.
var ErrNotFound = errors.New("passwordreset: not found")

// ErrExpired is returned when Get finds a row that hasn't been used
// but whose expires_at is in the past. Distinguished from ErrNotFound
// so the service can surface a "your reset link expired, request a
// new one" UX. Differentiating expired-vs-missing is acceptable here
// because an attacker holding an expired token already had it (e.g.
// from an email leak) — they learn nothing new from the distinction.
var ErrExpired = errors.New("passwordreset: expired")

// Entry mirrors a row in password_resets.
type Entry struct {
	TokenHash []byte
	UserID    uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// Queries is the per-aggregate repository.
type Queries struct {
	db  storage.DBTX
	now func() time.Time // injected so tests can pin the boundary
}

// New returns a Queries bound to db. now defaults to time.Now.
func New(db storage.DBTX) *Queries { return &Queries{db: db, now: time.Now} }

// NewWithClock is the test-injection escape hatch. The Get expiry
// boundary uses the same clock the service uses to write expires_at,
// so a synthetic now() in tests doesn't drift the read past the write.
func NewWithClock(db storage.DBTX, now func() time.Time) *Queries {
	if now == nil {
		now = time.Now
	}
	return &Queries{db: db, now: now}
}

// WithTx returns a Queries instance bound to tx (clock preserved).
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx, now: q.now} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :exec
INSERT INTO password_resets (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)`

// getSQL returns the row by hash regardless of expiry/used state. The
// service inspects used_at and expires_at to choose between
// ErrNotFound (used/missing — uniform for security) and ErrExpired
// (live but past expiry).
const getSQL = `-- name: Get :one
SELECT token_hash, user_id, expires_at, used_at
FROM password_resets
WHERE token_hash = $1`

const markUsedSQL = `-- name: MarkUsed :exec
UPDATE password_resets SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL`

// markAllUsedForUserSQL invalidates every live token for a given user.
// Used by the service before issuing a fresh reset token so only ONE
// token is active per user at any moment — resending the email
// silently kills the previous link.
const markAllUsedForUserSQL = `-- name: MarkAllUsedForUser :exec
UPDATE password_resets SET used_at = now()
WHERE user_id = $1 AND used_at IS NULL`

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

// Get loads the row by hash and classifies the result:
//   - missing or already used → ErrNotFound (uniform for security)
//   - exists, unused, but past expiry → ErrExpired
//   - exists, unused, not expired → Entry, nil
//
// Treating "missing" and "used" identically prevents an attacker from
// confirming a reused token was once valid. Expired is distinguished
// only because the legitimate user benefits from a clear "your link
// expired" message and the disclosure is harmless (an attacker with
// an expired token already had it).
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
	if e.UsedAt != nil {
		return Entry{}, ErrNotFound
	}
	if !e.ExpiresAt.After(q.now()) {
		return Entry{}, ErrExpired
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

// MarkAllUsedForUser invalidates every live (unused) reset token for
// the given user. Returns the number of rows affected for the caller's
// log line. The auth service calls this before creating a fresh token
// so only ONE token is active at a time — resending the reset email
// silently kills any earlier link the user might still try to click.
func (q *Queries) MarkAllUsedForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := q.db.Exec(ctx, markAllUsedForUserSQL, userID)
	if err != nil {
		return 0, fmt.Errorf("passwordreset: mark all used for user: %w", err)
	}
	return tag.RowsAffected(), nil
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
