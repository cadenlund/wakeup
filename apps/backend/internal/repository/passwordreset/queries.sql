-- queries.sql for the password_resets table (migration 0008).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Create :exec
INSERT INTO password_resets (token_hash, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: Get :one
-- Returns a row only if it has not expired AND has not been consumed.
-- Callers detect "wrong/expired/used token" uniformly via ErrNotFound.
SELECT token_hash, user_id, expires_at, used_at
FROM password_resets
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now();

-- name: MarkUsed :exec
UPDATE password_resets SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL;

-- name: DeleteExpiredAndUsed :execrows
-- Used by the §4.12 sweeper (separate from the Idempotency one). Removes
-- rows whose expiry has passed OR that have already been consumed.
DELETE FROM password_resets
WHERE expires_at <= now() OR used_at IS NOT NULL;
