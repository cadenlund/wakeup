// Package presence is the data-access layer for the presence_states
// table (migration 0007). UpsertHeartbeat / SetStatus follow the §9.2
// rules: heartbeat refreshes timestamps and demotes-then-restores
// `away` users; manual SetStatus is the only way to flip into
// `sleeping` or `offline`. The decay sweeper lives in the service
// layer and uses DecayStale.
package presence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadenlund/wakeup/apps/backend/internal/domain"
	"github.com/cadenlund/wakeup/apps/backend/internal/storage"
)

// ErrNotFound is the sentinel returned when a user has no
// presence_states row yet. Callers (the service layer) typically
// render that as `offline` rather than surfacing the error.
var ErrNotFound = errors.New("presence: not found")

// Queries is the per-aggregate repository. Goroutine-safe.
type Queries struct {
	db storage.DBTX
}

// New returns a Queries bound to db.
func New(db storage.DBTX) *Queries { return &Queries{db: db} }

// WithTx returns a Queries instance bound to tx for transactional
// composition.
func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

// SQL constants mirror queries.sql 1:1 (§4.3 discipline).

const getSQL = `-- name: Get :one
SELECT user_id, status, intent, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = $1`

const upsertHeartbeatSQL = `-- name: UpsertHeartbeat :one
INSERT INTO presence_states (user_id, status, last_active_at, last_heartbeat_at)
VALUES ($1, 'online', now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    last_active_at    = now(),
    last_heartbeat_at = now(),
    status = CASE
        WHEN presence_states.intent IS NOT NULL THEN presence_states.status
        WHEN presence_states.status = 'away' THEN 'online'
        ELSE presence_states.status
    END
RETURNING user_id, status, intent, last_active_at, last_heartbeat_at, updated_at`

const setStatusSQL = `-- name: SetStatus :one
INSERT INTO presence_states (user_id, status, intent, last_active_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id) DO UPDATE SET
    status         = EXCLUDED.status,
    intent         = EXCLUDED.intent,
    last_active_at = now()
RETURNING user_id, status, intent, last_active_at, last_heartbeat_at, updated_at`

const listByIDsSQL = `-- name: ListByIDs :many
SELECT user_id, status, intent, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = ANY($1::uuid[])`

const decayStaleSQL = `-- name: DecayStale :many
WITH demoted AS (
    UPDATE presence_states
    SET status = CASE
        WHEN status = 'online' AND last_active_at < now() - $1::interval THEN 'away'
        WHEN status = 'away'   AND last_active_at < now() - $2::interval THEN 'offline'
        ELSE status
    END
    WHERE intent IS NULL
      AND ((status = 'online' AND last_active_at < now() - $1::interval)
        OR (status = 'away'   AND last_active_at < now() - $2::interval))
    RETURNING user_id, status, intent, last_active_at, last_heartbeat_at, updated_at
)
SELECT * FROM demoted`

func scanPresence(row pgx.Row) (domain.PresenceState, error) {
	var p domain.PresenceState
	err := row.Scan(&p.UserID, &p.Status, &p.Intent, &p.LastActiveAt, &p.LastHeartbeatAt, &p.UpdatedAt)
	return p, err
}

// Get returns the presence row for userID, or ErrNotFound when the
// user has never heartbeat'd / set a status.
func (q *Queries) Get(ctx context.Context, userID uuid.UUID) (domain.PresenceState, error) {
	p, err := scanPresence(q.db.QueryRow(ctx, getSQL, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PresenceState{}, ErrNotFound
	}
	if err != nil {
		return domain.PresenceState{}, fmt.Errorf("presence: get: %w", err)
	}
	return p, nil
}

// UpsertHeartbeat refreshes timestamps for userID. If the row didn't
// exist it's created with status='online'; if status was 'away' it
// promotes back to 'online'; manual statuses ('sleeping', 'offline')
// are preserved.
func (q *Queries) UpsertHeartbeat(ctx context.Context, userID uuid.UUID) (domain.PresenceState, error) {
	p, err := scanPresence(q.db.QueryRow(ctx, upsertHeartbeatSQL, userID))
	if err != nil {
		return domain.PresenceState{}, fmt.Errorf("presence: upsert heartbeat: %w", err)
	}
	return p, nil
}

// SetStatus is the manual override. status is the new effective status
// (what other users see); intent is the sticky-override marker. Pass a
// non-nil intent equal to status to make the override sticky (DND,
// sleeping, away). Pass nil intent to clear an existing override —
// the next heartbeat / decay cycle takes back over.
func (q *Queries) SetStatus(ctx context.Context, userID uuid.UUID, status domain.PresenceStatus, intent *domain.PresenceStatus) (domain.PresenceState, error) {
	p, err := scanPresence(q.db.QueryRow(ctx, setStatusSQL, userID, status, intent))
	if err != nil {
		return domain.PresenceState{}, fmt.Errorf("presence: set status: %w", err)
	}
	return p, nil
}

// ListByIDs returns the presence rows for the given user IDs. Missing
// users (no row) are silently omitted — the service layer fills them
// in as 'offline' when rendering the §6.1 widget endpoint.
func (q *Queries) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.PresenceState, error) {
	if len(ids) == 0 {
		return []domain.PresenceState{}, nil
	}
	rows, err := q.db.Query(ctx, listByIDsSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("presence: list by ids: %w", err)
	}
	defer rows.Close()
	out := make([]domain.PresenceState, 0, len(ids))
	for rows.Next() {
		p, scanErr := scanPresence(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("presence: list by ids scan: %w", scanErr)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("presence: list by ids rows: %w", err)
	}
	return out, nil
}

// DecayStale runs the §9.2 sweeper: online → away after onlineCutoff,
// away → offline after awayCutoff. Returns every row the update
// touched (user_id + new status) so the service can publish a
// presence.update event for each.
func (q *Queries) DecayStale(ctx context.Context, onlineCutoff, awayCutoff time.Duration) ([]domain.PresenceState, error) {
	rows, err := q.db.Query(ctx, decayStaleSQL, onlineCutoff, awayCutoff)
	if err != nil {
		return nil, fmt.Errorf("presence: decay stale: %w", err)
	}
	defer rows.Close()
	var out []domain.PresenceState
	for rows.Next() {
		p, scanErr := scanPresence(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("presence: decay stale scan: %w", scanErr)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("presence: decay stale rows: %w", err)
	}
	return out, nil
}
