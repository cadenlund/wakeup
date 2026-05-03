// Package idempotency persists the cached responses backing the
// Idempotency-Key middleware (WAKEUP.md §4.8). Get returns a previously-stored
// response when the same key + user combo retries; Put stores the response so
// future retries can replay; DeleteExpired drives the §4.12 sweeper.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is returned by Get when no live (unexpired) row matches the
// (key, user_id) pair. Callers compare with errors.Is to drive miss/replay
// branching without inspecting pgx.ErrNoRows directly.
var ErrNotFound = errors.New("idempotency: entry not found")

// ErrConflict is returned by Put when a row already exists for
// (user_id, key). Surfaces the §4.8 race window where two concurrent
// requests both miss Get, both run the handler, and both try to Put —
// the loser sees this error and can fall back to Get + replay so the
// client receives the cached response from the winner.
var ErrConflict = errors.New("idempotency: entry already exists")

// uniqueViolationCode is Postgres SQLSTATE 23505. Detected via the
// driver's pgconn.PgError so we don't string-match the message text.
const uniqueViolationCode = "23505"

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
SELECT key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at
FROM idempotency_keys
WHERE key = $1 AND user_id = $2 AND expires_at > now()`

const insertEntry = `-- name: Insert :one
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_headers, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at`

const deleteExpired = `-- name: DeleteExpired :execrows
DELETE FROM idempotency_keys WHERE expires_at <= now()`

// scanRow decodes one idempotency_keys row, including the jsonb-encoded
// response_headers column. Centralized so Get / Put stay in sync on
// column order.
func scanRow(row pgx.Row) (Entry, error) {
	var (
		e           Entry
		headerBytes []byte
	)
	err := row.Scan(
		&e.Key,
		&e.UserID,
		&e.RequestHash,
		&e.ResponseStatus,
		&headerBytes,
		&e.ResponseBody,
		&e.CreatedAt,
		&e.ExpiresAt,
	)
	if err != nil {
		return Entry{}, err
	}
	if len(headerBytes) > 0 {
		if err := json.Unmarshal(headerBytes, &e.ResponseHeaders); err != nil {
			return Entry{}, fmt.Errorf("idempotency: decode headers: %w", err)
		}
	}
	return e, nil
}

// Get returns the Entry for (key, userID) if it exists and has not expired.
// Returns ErrNotFound on miss; any other error is a real DB failure.
func (q *Queries) Get(ctx context.Context, key string, userID uuid.UUID) (Entry, error) {
	e, err := scanRow(q.db.QueryRow(ctx, getByKeyAndUser, key, userID))
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
// string). A composite-PK violation surfaces as ErrConflict so callers
// (the §4.8 middleware) can detect concurrent inserts and fall back to
// Get → replay rather than failing the request.
func (q *Queries) Put(ctx context.Context, p PutParams) (Entry, error) {
	if p.TTL <= 0 {
		return Entry{}, fmt.Errorf("idempotency: PutParams.TTL must be > 0, got %s", p.TTL)
	}
	var headerBytes []byte
	if p.ResponseHeaders != nil {
		raw, err := json.Marshal(p.ResponseHeaders)
		if err != nil {
			return Entry{}, fmt.Errorf("idempotency: encode headers: %w", err)
		}
		headerBytes = raw
	}
	expiresAt := time.Now().UTC().Add(p.TTL)
	e, err := scanRow(q.db.QueryRow(ctx, insertEntry,
		p.Key, p.UserID, p.RequestHash, p.ResponseStatus, headerBytes, p.ResponseBody, expiresAt,
	))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return Entry{}, ErrConflict
		}
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
