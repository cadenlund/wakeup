-- queries.sql for the device_tokens table (migration 0009).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Register :one
-- Idempotent register: a re-register with the same (user_id, expo_token)
-- pair is an UPDATE-by-pair, not a duplicate row (the UNIQUE index in
-- migration 0009 enforces this). The platform is refreshed and last_seen_at
-- bumped to now() so callers can use it as a "this device is alive" signal.
-- Returns the row in both branches so callers always get an id.
INSERT INTO device_tokens (id, user_id, expo_token, platform)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, expo_token) DO UPDATE
SET platform     = EXCLUDED.platform,
    last_seen_at = now()
RETURNING id, user_id, expo_token, platform, created_at, last_seen_at;

-- name: Delete :execrows
-- Scoped delete: callers must pass both id and user_id so a stolen id
-- can't be used to remove another user's token. Returns rowcount so the
-- handler can map "0 rows" to a 404.
DELETE FROM device_tokens
WHERE id = $1 AND user_id = $2;

-- name: ListByUser :many
-- Returns all device tokens for a user, newest-first by last_seen_at.
-- Used by the §11 push-notification fan-out path.
SELECT id, user_id, expo_token, platform, created_at, last_seen_at
FROM device_tokens
WHERE user_id = $1
ORDER BY last_seen_at DESC, id DESC;
