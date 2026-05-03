-- queries.sql for the audit_log table (migration 0010).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Create :exec
-- Append-only: there's no Update or Delete in this repo by design.
-- Metadata is encoded as JSONB on the Go side (json.Marshal); $5 takes
-- a []byte that the driver casts to jsonb via the column type.
INSERT INTO audit_log (id, actor_id, action, target_type, target_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: List :many
-- Newest-first keyset on (created_at DESC, id DESC). Like the §11.5
-- message list, we over-fetch limit+1 so the service layer can compute
-- has_more / next_cursor via pagination.Page. The cursor row itself
-- is excluded via the strict (<, <) tuple comparison.
SELECT id, actor_id, action, target_type, target_id, metadata, created_at
FROM audit_log
WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1, $2))
ORDER BY created_at DESC, id DESC
LIMIT $3;
