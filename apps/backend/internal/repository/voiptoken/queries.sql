-- queries.sql for voip_tokens (migration 0009). Per-user iOS PushKit
-- VoIP tokens. Re-register on the (user_id, voip_token) pair updates
-- last_seen_at via ON CONFLICT — same idempotency posture as Expo
-- tokens, just a different transport.

-- name: Register :one
INSERT INTO voip_tokens (id, user_id, voip_token)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, voip_token) DO UPDATE
SET last_seen_at = now()
RETURNING id, user_id, voip_token, created_at, last_seen_at;

-- name: Delete :execrows
-- Scoped to (id, user_id) so a stolen token id from another user
-- can't delete that user's row — no enumeration leak.
DELETE FROM voip_tokens
WHERE id = $1 AND user_id = $2;

-- name: ListByUser :many
SELECT id, user_id, voip_token, created_at, last_seen_at
FROM voip_tokens
WHERE user_id = $1
ORDER BY created_at DESC;
