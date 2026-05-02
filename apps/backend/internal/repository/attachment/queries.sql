-- queries.sql for the attachments table family (migration 0006).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Create :one
INSERT INTO attachments (id, uploader_id, storage_key, filename, content_type, size_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, uploader_id, storage_key, filename, content_type, size_bytes, created_at;

-- name: GetByID :one
SELECT id, uploader_id, storage_key, filename, content_type, size_bytes, created_at
FROM attachments
WHERE id = $1;

-- name: ListOrphansOlderThan :many
-- Used by the §9.6 orphan sweeper. An "orphan" is an attachment with no
-- message_attachments row (never linked to a message) AND created_at <
-- the cutoff (default 24h old). Ordered ASC so a partial sweep makes
-- forward progress on the oldest first.
SELECT a.id, a.uploader_id, a.storage_key, a.filename, a.content_type, a.size_bytes, a.created_at
FROM attachments a
LEFT JOIN message_attachments ma ON ma.attachment_id = a.id
WHERE ma.attachment_id IS NULL
  AND a.created_at < $1
ORDER BY a.created_at ASC;

-- name: DeleteByIDs :exec
-- Bulk delete by id list. Used by the orphan sweeper after the S3 object
-- has been removed. The NOT EXISTS guard is defense-in-depth: between
-- ListOrphansOlderThan() and this delete, a slow client might finish
-- composing and insert a message_attachments row. Without the guard, we
-- would unconditionally delete the attachment AND its just-created link
-- via the ON DELETE CASCADE FK, silently dropping a real attachment from
-- a real message. Filtering here means the worst case is the row stays
-- for the next sweeper tick (the S3 object is gone — see service-layer
-- ordering), which is a recoverable state.
DELETE FROM attachments
WHERE id = ANY($1::uuid[])
  AND NOT EXISTS (
      SELECT 1 FROM message_attachments ma WHERE ma.attachment_id = attachments.id
  );

-- name: CallerCanRead :one
-- Returns true iff one of:
--   1) a `message_attachments` row exists for this attachment AND its
--      message lives in a conversation the caller is a member of, OR
--   2) the attachment has zero `message_attachments` rows AND
--      uploader_id == caller (orphan-during-compose case, §9.3).
--
-- Single SELECT so the handler can ask one question and get back one
-- bool — no service-layer fan-out.
SELECT EXISTS (
    SELECT 1
    FROM attachments a
    JOIN message_attachments ma ON ma.attachment_id = a.id
    JOIN messages m             ON m.id = ma.message_id
    JOIN conversation_members cm ON cm.conversation_id = m.conversation_id
    WHERE a.id = $1 AND cm.user_id = $2
) OR EXISTS (
    SELECT 1
    FROM attachments a
    WHERE a.id = $1
      AND a.uploader_id = $2
      AND NOT EXISTS (SELECT 1 FROM message_attachments ma WHERE ma.attachment_id = a.id)
) AS can_read;
