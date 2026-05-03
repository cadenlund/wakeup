-- queries.sql for presence_states (migration 0007). Constants in
-- repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Get :one
SELECT user_id, status, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = $1;

-- name: UpsertHeartbeat :one
-- Refreshes the WS heartbeat AND last_active_at, and bumps the user
-- to `online` if they were `away` (the §9.2 decay rule treats any
-- heartbeat as evidence the user came back). Manual `sleeping` and
-- `offline` overrides stick — heartbeat doesn't override an explicit
-- status set via SetStatus.
INSERT INTO presence_states (user_id, status, last_active_at, last_heartbeat_at)
VALUES ($1, 'online', now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    last_active_at    = now(),
    last_heartbeat_at = now(),
    status = CASE
        WHEN presence_states.status = 'away' THEN 'online'
        ELSE presence_states.status
    END
RETURNING user_id, status, last_active_at, last_heartbeat_at, updated_at;

-- name: SetStatus :one
-- Manual status override (e.g. user toggles `sleeping`). Also bumps
-- last_active_at so the decay sweeper doesn't immediately demote.
INSERT INTO presence_states (user_id, status, last_active_at)
VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE SET
    status         = EXCLUDED.status,
    last_active_at = now()
RETURNING user_id, status, last_active_at, last_heartbeat_at, updated_at;

-- name: ListByIDs :many
-- Bulk lookup for the §6.1 GET /v1/presence/friends endpoint. Returns
-- whatever rows exist; missing user_ids surface as "no row" — the
-- service layer renders them as `offline` defaults.
SELECT user_id, status, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = ANY($1::uuid[]);

-- name: DecayStale :many
-- The §9.2 sweeper rule: rows with status='online' and
-- last_active_at < now() - $1 (5min) move to 'away';
-- rows with status='away' and last_active_at < now() - $2 (1h) move
-- to 'offline'. Returns every row the sweeper touched so the service
-- can publish presence.update events.
WITH demoted AS (
    UPDATE presence_states
    SET status = CASE
        WHEN status = 'online' AND last_active_at < now() - $1::interval THEN 'away'
        WHEN status = 'away'   AND last_active_at < now() - $2::interval THEN 'offline'
        ELSE status
    END
    WHERE (status = 'online' AND last_active_at < now() - $1::interval)
       OR (status = 'away'   AND last_active_at < now() - $2::interval)
    RETURNING user_id, status, last_active_at, last_heartbeat_at, updated_at
)
SELECT * FROM demoted;
