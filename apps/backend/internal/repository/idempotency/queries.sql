-- queries.sql for the idempotency_keys table. Constants in repo.go MUST mirror
-- these SQL bodies verbatim — see WAKEUP.md §4.3 for the discipline rule.

-- name: GetByKeyAndUser :one
SELECT key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at
FROM idempotency_keys
WHERE key = $1 AND user_id = $2 AND expires_at > now();

-- name: Insert :one
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_headers, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at;

-- name: Reserve :one
-- Atomic reservation that creates an "in-flight" placeholder row before
-- the handler runs (response_status = 0 is the sentinel — no real HTTP
-- status uses 0). On conflict the existing row is returned via the second
-- statement so the middleware can replay or short-circuit. Without this
-- two concurrent requests with the same (user_id, key) could both miss
-- Get-then-Insert and both run the handler before one Insert loses,
-- producing duplicate side effects.
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_body, expires_at)
VALUES ($1, $2, $3, 0, ''::bytea, $4)
ON CONFLICT (user_id, key) DO NOTHING
RETURNING key, user_id, request_hash, response_status, response_headers, response_body, created_at, expires_at;

-- name: Complete :execrows
-- Replaces an in-flight placeholder with the real (status, headers, body).
-- The WHERE clause includes response_status = 0 so a row that was already
-- completed by another writer (or rolled back to a placeholder somehow)
-- isn't silently overwritten.
UPDATE idempotency_keys
SET response_status  = $3,
    response_headers = $4,
    response_body    = $5,
    expires_at       = $6
WHERE user_id = $1 AND key = $2 AND response_status = 0;

-- name: DeleteByKey :execrows
-- Drops a row by (user_id, key). Used by the §4.8 5xx path to clear
-- the in-flight placeholder so a retry isn't blocked by a stale row.
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: DeleteExpired :execrows
-- Use <= so the boundary case (expires_at == now()) is removed too — matches
-- GetByKeyAndUser's strict `> now()` filter, which already treats the
-- boundary as expired/invisible.
DELETE FROM idempotency_keys WHERE expires_at <= now();
