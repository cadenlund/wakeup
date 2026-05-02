// Package idempotency persists the cached responses backing the
// Idempotency-Key middleware (WAKEUP.md §4.8). Get returns a previously-stored
// response when the same key + user combo retries; Put stores the response so
// future retries can replay; DeleteExpired drives the §4.12 sweeper.
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is returned by Get when no live (unexpired) row matches the
// (key, user_id) pair. Callers compare with errors.Is to drive miss/replay
// branching without inspecting pgx.ErrNoRows directly.
var ErrNotFound = errors.New("idempotency: entry not found")

// Queries is the per-aggregate repository. Holds a DBTX so the same struct
// works against either the connection pool or a transaction.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db. The caller owns db's lifecycle.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to the given transaction so a
// service can call several repos atomically. See WAKEUP.md §4.2.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1. CI grep-checks the `-- name:` headers
// are kept in sync with this file (see §4.3).

const getByKeyAndUser = `-- name: GetByKeyAndUser :one
SELECT key, user_id, request_hash, response_status, response_body, created_at, expires_at
FROM idempotency_keys
WHERE key = $1 AND user_id = $2 AND expires_at > now()`

const insertEntry = `-- name: Insert :one
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING key, user_id, request_hash, response_status, response_body, created_at, expires_at`

const deleteExpired = `-- name: DeleteExpired :execrows
DELETE FROM idempotency_keys WHERE expires_at <= now()`

// Get returns the Entry for (key, userID) if it exists and has not expired.
// Returns ErrNotFound on miss; any other error is a real DB failure.
func (q *Queries) Get(ctx context.Context, key string, userID uuid.UUID) (Entry, error) {
	row := q.db.QueryRow(ctx, getByKeyAndUser, key, userID)
	var e Entry
	err := row.Scan(
		&e.Key,
		&e.UserID,
		&e.RequestHash,
		&e.ResponseStatus,
		&e.ResponseBody,
		&e.CreatedAt,
		&e.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("idempotency: get: %w", err)
	}
	return e, nil
}

// Put inserts a new entry. expires_at is computed as now() + p.TTL on the
// application side (so the DB row receives a timestamptz, not an interval
// string). A composite-PK violation propagates back as the underlying pgx
// error so the middleware can detect concurrent inserts.
func (q *Queries) Put(ctx context.Context, p PutParams) (Entry, error) {
	if p.TTL <= 0 {
		return Entry{}, fmt.Errorf("idempotency: PutParams.TTL must be > 0, got %s", p.TTL)
	}
	expiresAt := time.Now().UTC().Add(p.TTL)
	row := q.db.QueryRow(ctx, insertEntry,
		p.Key, p.UserID, p.RequestHash, p.ResponseStatus, p.ResponseBody, expiresAt,
	)
	var e Entry
	err := row.Scan(
		&e.Key,
		&e.UserID,
		&e.RequestHash,
		&e.ResponseStatus,
		&e.ResponseBody,
		&e.CreatedAt,
		&e.ExpiresAt,
	)
	if err != nil {
		return Entry{}, fmt.Errorf("idempotency: put: %w", err)
	}
	return e, nil
}

// DeleteExpired removes every row whose expires_at is in the past. Returns
// the number of rows deleted so the §4.12 sweeper can log meaningful counts.
func (q *Queries) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, deleteExpired)
	if err != nil {
		return 0, fmt.Errorf("idempotency: delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
