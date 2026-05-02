-- queries.sql for the idempotency_keys table. Constants in repo.go MUST mirror
-- these SQL bodies verbatim — see WAKEUP.md §4.3 for the discipline rule.

-- name: GetByKeyAndUser :one
SELECT key, user_id, request_hash, response_status, response_body, created_at, expires_at
FROM idempotency_keys
WHERE key = $1 AND user_id = $2 AND expires_at > now();

-- name: Insert :one
INSERT INTO idempotency_keys (key, user_id, request_hash, response_status, response_body, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING key, user_id, request_hash, response_status, response_body, created_at, expires_at;

-- name: DeleteExpired :execrows
-- Use <= so the boundary case (expires_at == now()) is removed too — matches
-- GetByKeyAndUser's strict `> now()` filter, which already treats the
-- boundary as expired/invisible.
DELETE FROM idempotency_keys WHERE expires_at <= now();
