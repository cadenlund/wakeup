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

const reserveSQL = `-- name: Reserve :one
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_body, expires_at)
VALUES ($1, $2, $3, 0, ''::bytea, $4)
ON CONFLICT (user_id, key) DO NOTHING
RETURNING key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at`

const completeSQL = `-- name: Complete :execrows
UPDATE idempotency_keys
SET response_status  = $3,
    response_headers = $4,
    response_body    = $5,
    expires_at       = $6
WHERE user_id = $1 AND key = $2 AND response_status = 0`

const deleteByKeySQL = `-- name: DeleteByKey :execrows
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2`

// PlaceholderStatus is the response_status value Reserve writes for an
// in-flight entry. 0 is not a valid HTTP status, so callers can use
// `entry.ResponseStatus == PlaceholderStatus` to tell a fresh
// reservation from a completed cached row.
const PlaceholderStatus = 0

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

// ReserveParams is the input to Reserve. Mirrors PutParams's required
// fields except for the response payload (Reserve writes a placeholder
// status; Complete fills in the real response later).
type ReserveParams struct {
	Key         string
	UserID      uuid.UUID
	RequestHash []byte
	TTL         time.Duration
}

// Reserve attempts to atomically claim (key, userID) by inserting a
// placeholder row with response_status = PlaceholderStatus. Returns the
// reserved entry and ok=true on success. If the row already exists the
// existing entry is fetched and returned with ok=false — callers
// inspect ResponseStatus to tell a complete-cached entry (real status)
// from another in-flight reservation (PlaceholderStatus).
//
// This is the at-most-once primitive the §4.8 middleware uses so two
// concurrent retries of the same key can't both run the handler before
// one cache write loses (CodeRabbit raised this on PR #74).
func (q *Queries) Reserve(ctx context.Context, p ReserveParams) (Entry, bool, error) {
	if p.TTL <= 0 {
		return Entry{}, false, fmt.Errorf("idempotency: ReserveParams.TTL must be > 0, got %s", p.TTL)
	}
	expiresAt := time.Now().UTC().Add(p.TTL)
	e, err := scanRow(q.db.QueryRow(ctx, reserveSQL, p.Key, p.UserID, p.RequestHash, expiresAt))
	if err == nil {
		return e, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict: row already exists. Fetch it so the caller can decide.
		existing, getErr := q.Get(ctx, p.Key, p.UserID)
		if getErr != nil {
			return Entry{}, false, getErr
		}
		return existing, false, nil
	}
	return Entry{}, false, fmt.Errorf("idempotency: reserve: %w", err)
}

// CompleteParams is the input to Complete.
type CompleteParams struct {
	Key             string
	UserID          uuid.UUID
	ResponseStatus  int
	ResponseHeaders map[string][]string
	ResponseBody    []byte
	TTL             time.Duration
}

// Complete updates an in-flight placeholder with the real response.
// Returns the underlying rowcount (0 if there was no placeholder to
// replace, e.g. another writer already completed it). Callers usually
// log a 0-rowcount as a soft warning rather than an error.
func (q *Queries) Complete(ctx context.Context, p CompleteParams) (int64, error) {
	if p.TTL <= 0 {
		return 0, fmt.Errorf("idempotency: CompleteParams.TTL must be > 0, got %s", p.TTL)
	}
	var headerBytes []byte
	if p.ResponseHeaders != nil {
		raw, err := json.Marshal(p.ResponseHeaders)
		if err != nil {
			return 0, fmt.Errorf("idempotency: encode headers: %w", err)
		}
		headerBytes = raw
	}
	expiresAt := time.Now().UTC().Add(p.TTL)
	tag, err := q.db.Exec(ctx, completeSQL,
		p.UserID, p.Key, p.ResponseStatus, headerBytes, p.ResponseBody, expiresAt,
	)
	if err != nil {
		return 0, fmt.Errorf("idempotency: complete: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteByKey removes the row at (user_id, key). Used by the §4.8 5xx
// path to clear an in-flight placeholder so client retries aren't
// blocked by a stale reservation.
func (q *Queries) DeleteByKey(ctx context.Context, key string, userID uuid.UUID) (int64, error) {
	tag, err := q.db.Exec(ctx, deleteByKeySQL, userID, key)
	if err != nil {
		return 0, fmt.Errorf("idempotency: delete by key: %w", err)
	}
	return tag.RowsAffected(), nil
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
