-- queries.sql for presence_states (migration 0007). Constants in
-- repo.go MUST mirror these SQL bodies verbatim (§4.3).
--
-- Column order on every SELECT/RETURNING: user_id, status, intent,
-- last_active_at, last_heartbeat_at, updated_at. scanPresence in repo.go
-- assumes this exact order — keep them in lock-step.
--
-- intent is the user-set sticky override (WAKEUP.md §6.2 / §10.2). When
-- non-null, the heartbeat upsert and the decay sweeper LEAVE STATUS ALONE
-- — the user's manual choice survives WS disconnect / decay so DND, the
-- most common case, doesn't reset the moment the app backgrounds.

-- name: Get :one
SELECT user_id, status, intent, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = $1;

-- name: UpsertHeartbeat :one
-- Refreshes the WS heartbeat AND last_active_at, and bumps the user
-- to `online` if they were `away` — UNLESS intent is set, in which
-- case status is sticky and we leave it alone.
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
RETURNING user_id, status, intent, last_active_at, last_heartbeat_at, updated_at;

-- name: SetStatus :one
-- Manual status override. $2 = new effective status (must be in the
-- enum); $3 = new intent value (NULL clears the sticky override). The
-- service layer enforces that $2 == $3 when $3 is non-null and
-- substitutes a sensible default ('online') when $3 is null.
INSERT INTO presence_states (user_id, status, intent, last_active_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id) DO UPDATE SET
    status         = EXCLUDED.status,
    intent         = EXCLUDED.intent,
    last_active_at = now()
RETURNING user_id, status, intent, last_active_at, last_heartbeat_at, updated_at;

-- name: ListByIDs :many
-- Bulk lookup for the §6.1 GET /v1/presence/friends endpoint. Returns
-- whatever rows exist; missing user_ids surface as "no row" — the
-- service layer renders them as `offline` defaults.
SELECT user_id, status, intent, last_active_at, last_heartbeat_at, updated_at
FROM presence_states
WHERE user_id = ANY($1::uuid[]);

-- name: DecayStale :many
-- The §9.2 sweeper rule. Skipped entirely for users with intent set —
-- their sticky DND/sleeping shouldn't decay back to away/offline.
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
SELECT * FROM demoted;
