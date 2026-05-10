// Package friendship is the data-access layer for the friendships table
// (migration 0003). The state-machine values live in
// `internal/domain/friendship.go`; this package handles SQL only.
//
// Direction matters at the row level (requester vs addressee), but the
// pair-unique index normalizes ordering so any (A,B) lookup also
// matches (B,A) — see GetByPair / DeleteByPair.
package friendship

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/pagination"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is the sentinel returned when a row doesn't exist. Callers
// compare with errors.Is.
var ErrNotFound = errors.New("friendship: not found")

// Queries is the per-aggregate repository. Goroutine-safe.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx so the service layer can
// compose multiple repos in one transaction (§4.2).
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// CreateParams is the input to Create. ID is the v7 UUID the service
// pre-generated; Status carries the initial state ("pending" for a
// friend request, "blocked" when SendingBlock).
type CreateParams struct {
	ID          uuid.UUID
	RequesterID uuid.UUID
	AddresseeID uuid.UUID
	Status      domain.FriendshipStatus
}

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const createSQL = `-- name: Create :one
INSERT INTO friendships (id, requester_id, addressee_id, status)
VALUES ($1, $2, $3, $4)
RETURNING id, requester_id, addressee_id, status, created_at, accepted_at`

const getByIDSQL = `-- name: GetByID :one
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE id = $1`

const getByPairSQL = `-- name: GetByPair :one
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE LEAST(requester_id, addressee_id) = LEAST($1::uuid, $2::uuid)
  AND GREATEST(requester_id, addressee_id) = GREATEST($1::uuid, $2::uuid)`

const acceptSQL = `-- name: Accept :one
UPDATE friendships
SET status = 'accepted',
    accepted_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING id, requester_id, addressee_id, status, created_at, accepted_at`

const deleteSQL = `-- name: Delete :exec
DELETE FROM friendships WHERE id = $1`

// deletePendingByRequesterSQL atomically deletes a row only when
// it's still pending AND owned by the given requester. Used by
// CancelRequest so the addressee can't accept the row out from
// under us mid-flow — the equivalent service-level read-then-
// delete is racy.
const deletePendingByRequesterSQL = `-- name: DeletePendingByRequester :exec
DELETE FROM friendships
WHERE id = $1
  AND requester_id = $2
  AND status = 'pending'`

const deleteByPairSQL = `-- name: DeleteByPair :exec
DELETE FROM friendships
WHERE LEAST(requester_id, addressee_id) = LEAST($1::uuid, $2::uuid)
  AND GREATEST(requester_id, addressee_id) = GREATEST($1::uuid, $2::uuid)`

const listAcceptedByUserSQL = `-- name: ListAcceptedByUser :many
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE status = 'accepted'
  AND (requester_id = $1 OR addressee_id = $1)
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (accepted_at, id))
ORDER BY accepted_at DESC, id DESC
LIMIT $4`

// countAcceptedByUserSQL mirrors listAcceptedByUserSQL minus the
// keyset cursor — returns the absolute count of accepted friend
// rows touching the user. Drives the friends-tab "X of N" hint.
const countAcceptedByUserSQL = `-- name: CountAcceptedByUser :one
SELECT COUNT(*)
FROM friendships
WHERE status = 'accepted'
  AND (requester_id = $1 OR addressee_id = $1)`

const listAllAcceptedFriendIDsSQL = `-- name: ListAllAcceptedFriendIDs :many
SELECT CASE WHEN requester_id = $1 THEN addressee_id ELSE requester_id END AS friend_id
FROM friendships
WHERE status = 'accepted'
  AND (requester_id = $1 OR addressee_id = $1)`

const listPendingByUserSQL = `-- name: ListPendingByUser :many
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE status = 'pending'
  AND (requester_id = $1 OR addressee_id = $1)
ORDER BY created_at DESC, id DESC`

const listBlockedByUserSQL = `-- name: ListBlockedByUser :many
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE status = 'blocked' AND requester_id = $1
ORDER BY created_at DESC, id DESC`

// scanRow decodes a single row into domain.Friendship. Centralized so
// column order stays consistent across queries.
func scanRow(row pgx.Row) (domain.Friendship, error) {
	var f domain.Friendship
	err := row.Scan(
		&f.ID,
		&f.RequesterID,
		&f.AddresseeID,
		&f.Status,
		&f.CreatedAt,
		&f.AcceptedAt,
	)
	return f, err
}

// Create inserts a new friendship row and returns it.
//
// Initial status MUST be `pending` or `blocked` — the `accepted` state
// is reachable only via the Accept() transition, which stamps
// accepted_at atomically. Inserting an accepted row with a NULL
// accepted_at would silently break the audit trail downstream
// (CodeRabbit caught this on PR #30).
//
// The pair-unique index in migration 0003 enforces that no other row
// exists between the same two users in either direction — duplicates
// surface as a Postgres unique-violation (SQLSTATE 23505) which the
// service layer maps to apierror.Conflict.
func (q *Queries) Create(ctx context.Context, p CreateParams) (domain.Friendship, error) {
	if p.Status != domain.FriendshipPending && p.Status != domain.FriendshipBlocked {
		return domain.Friendship{}, fmt.Errorf("friendship: create: invalid initial status %q (must be pending or blocked)", p.Status)
	}
	f, err := scanRow(q.db.QueryRow(ctx, createSQL,
		p.ID, p.RequesterID, p.AddresseeID, string(p.Status)))
	if err != nil {
		return domain.Friendship{}, fmt.Errorf("friendship: create: %w", err)
	}
	return f, nil
}

// GetByID returns the friendship with the given id. ErrNotFound if no
// such row.
func (q *Queries) GetByID(ctx context.Context, id uuid.UUID) (domain.Friendship, error) {
	f, err := scanRow(q.db.QueryRow(ctx, getByIDSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Friendship{}, ErrNotFound
	}
	if err != nil {
		return domain.Friendship{}, fmt.Errorf("friendship: get by id: %w", err)
	}
	return f, nil
}

// GetByPair returns the friendship between users a and b in either
// direction. ErrNotFound when no row exists.
func (q *Queries) GetByPair(ctx context.Context, a, b uuid.UUID) (domain.Friendship, error) {
	f, err := scanRow(q.db.QueryRow(ctx, getByPairSQL, a, b))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Friendship{}, ErrNotFound
	}
	if err != nil {
		return domain.Friendship{}, fmt.Errorf("friendship: get by pair: %w", err)
	}
	return f, nil
}

// Accept transitions a pending friendship to accepted and stamps
// accepted_at. Returns ErrNotFound when no PENDING row matches id —
// that includes already-accepted, already-blocked, or missing rows
// (the service layer can call GetByID first to disambiguate if needed).
func (q *Queries) Accept(ctx context.Context, id uuid.UUID) (domain.Friendship, error) {
	f, err := scanRow(q.db.QueryRow(ctx, acceptSQL, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Friendship{}, ErrNotFound
	}
	if err != nil {
		return domain.Friendship{}, fmt.Errorf("friendship: accept: %w", err)
	}
	return f, nil
}

// Delete removes the row with the given id. Used by decline (after
// confirming status='pending') and unfriend (after confirming
// status='accepted'). The repo doesn't enforce status — the service
// layer is responsible.
func (q *Queries) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := q.db.Exec(ctx, deleteSQL, id)
	if err != nil {
		return fmt.Errorf("friendship: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByPair removes any friendship between a and b in either
// direction. Idempotent — returns nil when no row exists. Used by
// Unblock (which doesn't have an id handy) and as a Block-then-recreate
// helper.
func (q *Queries) DeleteByPair(ctx context.Context, a, b uuid.UUID) error {
	if _, err := q.db.Exec(ctx, deleteByPairSQL, a, b); err != nil {
		return fmt.Errorf("friendship: delete by pair: %w", err)
	}
	return nil
}

// DeletePendingByRequester atomically deletes a friendship row only
// when it's still `pending` AND owned by `requester`. Used by
// CancelRequest so an in-flight Accept can't slip through the
// service's read-then-delete window. Returns ErrNotFound when no
// row matched (already accepted, declined, or owned by someone
// else); callers can treat that as "request was no longer
// cancelable" — distinct from a generic delete failure.
func (q *Queries) DeletePendingByRequester(ctx context.Context, id, requester uuid.UUID) error {
	tag, err := q.db.Exec(ctx, deletePendingByRequesterSQL, id, requester)
	if err != nil {
		return fmt.Errorf("friendship: delete pending: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAllAcceptedFriendIDs returns the user_id of every accepted
// friend, unpaginated. Used by the §9 presence service for fan-out
// (presence.update fires for friends only, so we need every friend
// at once — pagination would force the publisher into an N-page walk
// per state change). The friend graph is bounded by user behavior;
// realistic upper bound is in the hundreds.
func (q *Queries) ListAllAcceptedFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, listAllAcceptedFriendIDsSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("friendship: list accepted friend ids: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("friendship: list accepted friend ids scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("friendship: list accepted friend ids rows: %w", err)
	}
	return out, nil
}

// ListAcceptedByUser returns the user's accepted friendships ordered by
// (accepted_at DESC, id DESC). Pass cursor=nil for the first page.
//
// Always over-fetches limit+1 so the service layer can call
// pagination.Page to compute next_cursor + has_more.
func (q *Queries) ListAcceptedByUser(ctx context.Context, userID uuid.UUID, cursor *pagination.Cursor, limit int) ([]domain.Friendship, error) {
	if limit <= 0 {
		limit = pagination.DefaultLimit
	}
	overFetch := limit + 1

	var ts *time.Time
	var id *uuid.UUID
	if cursor != nil {
		ts = &cursor.Timestamp
		id = &cursor.ID
	}

	rows, err := q.db.Query(ctx, listAcceptedByUserSQL, userID, ts, id, overFetch)
	if err != nil {
		return nil, fmt.Errorf("friendship: list accepted: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Friendship, 0, overFetch)
	for rows.Next() {
		f, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("friendship: list accepted scan: %w", scanErr)
		}
		out = append(out, f)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("friendship: list accepted rows: %w", rowsErr)
	}
	return out, nil
}

// CountAcceptedByUser returns the absolute number of accepted
// friendships touching the user. Same WHERE clause as
// ListAcceptedByUser minus the keyset cursor — drives the
// friends-tab "X of N" hint.
func (q *Queries) CountAcceptedByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	if err := q.db.QueryRow(ctx, countAcceptedByUserSQL, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("friendship: count accepted: %w", err)
	}
	return n, nil
}

// ListPendingByUser returns every pending friendship where the user is
// either the requester or the addressee. The service layer separates
// incoming vs outgoing by comparing requester_id to userID.
func (q *Queries) ListPendingByUser(ctx context.Context, userID uuid.UUID) ([]domain.Friendship, error) {
	rows, err := q.db.Query(ctx, listPendingByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("friendship: list pending: %w", err)
	}
	defer rows.Close()

	var out []domain.Friendship
	for rows.Next() {
		f, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("friendship: list pending scan: %w", scanErr)
		}
		out = append(out, f)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("friendship: list pending rows: %w", rowsErr)
	}
	return out, nil
}

// ListBlockedByUser returns rows where userID has BLOCKED another user
// — only the blocker sees their block list (the addressee is unaware
// they were blocked, by design). Used by GET /v1/blocks.
func (q *Queries) ListBlockedByUser(ctx context.Context, userID uuid.UUID) ([]domain.Friendship, error) {
	rows, err := q.db.Query(ctx, listBlockedByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("friendship: list blocked: %w", err)
	}
	defer rows.Close()

	var out []domain.Friendship
	for rows.Next() {
		f, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("friendship: list blocked scan: %w", scanErr)
		}
		out = append(out, f)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("friendship: list blocked rows: %w", rowsErr)
	}
	return out, nil
}
