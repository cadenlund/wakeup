-- queries.sql for the users table. Constants in repo.go MUST mirror these
-- SQL bodies verbatim (§4.3 discipline).

-- name: Create :one
INSERT INTO users (id, username, display_name, email, password_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at;

-- name: GetByID :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetByIDIncludingDeleted :one
-- §4.6 soft-delete: this lookup is what message-history rendering uses to
-- still attribute messages to a soft-deleted author. Handlers / DTOs are
-- responsible for collapsing the user to "Deleted User".
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = $1;

-- name: GetByUsername :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE username = $1 AND deleted_at IS NULL;

-- name: GetByEmail :one
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: Update :one
-- COALESCE pattern lets the service patch only the fields it cares about.
-- Pass NULL for fields that should stay unchanged. avatar_url accepts both
-- NULL (don't change) and '' (clear) — the service maps those to two
-- different parameter shapes if it ever needs to clear the avatar.
UPDATE users
SET display_name = COALESCE($2, display_name),
    avatar_url   = COALESCE($3, avatar_url),
    color_scheme = COALESCE($4, color_scheme)
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateRole :exec
UPDATE users SET role = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDelete :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: ListByPrefix :many
-- Trigram-indexed prefix search on (username, display_name). The pg_trgm
-- GIN index accelerates `ILIKE 'prefix%'` patterns against either column
-- (Postgres 9.1+). q="" returns all (non-deleted) users.
-- $2/$3 are the (created_at, id) keyset cursor; pass NULL for first page.
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE deleted_at IS NULL
  AND (
    $1::text = ''
    OR username ILIKE $1::text || '%'
    OR display_name ILIKE $1::text || '%'
  )
  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $4;

-- name: ListByIDs :many
SELECT id, username, display_name, email, password_hash, avatar_url, color_scheme, role, created_at, updated_at, deleted_at
FROM users
WHERE id = ANY($1::uuid[]);
