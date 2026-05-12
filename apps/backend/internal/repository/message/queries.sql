-- queries.sql for the messages table family (migration 0005). Constants
-- in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Create :one
INSERT INTO messages (id, conversation_id, sender_id, body, reply_to_message_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, conversation_id, sender_id, body, reply_to_message_id,
          created_at, edited_at, deleted_at;

-- name: GetByID :one
-- Excludes soft-deleted rows. Service-layer calls this for normal reads.
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetByIDIncludingDeleted :one
-- Includes soft-deleted rows. Used when rendering history (§4.6) where
-- deleted-message placeholders must still surface their sender id.
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE id = $1;

-- name: UpdateBody :one
-- Owner-only edit. Stamps edited_at and refuses to touch a soft-deleted
-- row (caller must check ErrNotFound and decide whether to surface 404
-- or "deleted, can't edit" — the service layer does the latter).
UPDATE messages
SET body = $2, edited_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, conversation_id, sender_id, body, reply_to_message_id,
          created_at, edited_at, deleted_at;

-- name: SoftDelete :exec
-- Idempotent: re-deleting a soft-deleted row is a no-op (deleted_at
-- only stamps the first time).
UPDATE messages
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListByConversation :many
-- Reverse-chronological page within one conversation. Soft-deleted
-- rows are INCLUDED so the §4.6 placeholder can render — handlers
-- collapse `body` at the DTO boundary.
--
-- $5 (search query) is optional; when empty we skip the full-text
-- match. When set, applies plainto_tsquery against the body_tsv
-- generated column (§4.6 search semantics).
SELECT id, conversation_id, sender_id, body, reply_to_message_id,
       created_at, edited_at, deleted_at
FROM messages
WHERE conversation_id = $1
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (created_at, id))
  AND ($5::text = '' OR body_tsv @@ plainto_tsquery('english', $5))
ORDER BY created_at DESC, id DESC
LIMIT $4;

-- name: AddAttachment :exec
-- Idempotent: PK collision on a re-link is the same as success.
INSERT INTO message_attachments (message_id, attachment_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListAttachmentsForMessage :many
SELECT attachment_id
FROM message_attachments
WHERE message_id = $1
ORDER BY attachment_id;

-- name: MarkRead :exec
-- Idempotent: ON CONFLICT DO NOTHING means re-marking is a no-op (the
-- first read_at wins).
INSERT INTO message_reads (message_id, user_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListReadsForMessage :many
-- Returns every (user_id, read_at) for the message, newest read first.
SELECT message_id, user_id, read_at
FROM message_reads
WHERE message_id = $1
ORDER BY read_at DESC, user_id;

-- name: SearchInUserConversations :many
-- Cross-conversation full-text search restricted to conversations the
-- user is a member of. Powers GET /v1/search (mobile §5.1 global
-- search). Soft-deleted messages excluded; results ordered by recency
-- with a hard limit so the handler can render fast.
SELECT m.id, m.conversation_id, m.sender_id, m.body, m.reply_to_message_id,
       m.created_at, m.edited_at, m.deleted_at
FROM messages m
JOIN conversation_members cm ON cm.conversation_id = m.conversation_id AND cm.user_id = $1
WHERE m.deleted_at IS NULL
  AND m.body_tsv @@ plainto_tsquery('english', $2::text)
ORDER BY m.created_at DESC, m.id DESC
LIMIT $3;

-- name: CountUnreadForUser :one
-- Sum of unread messages across every conversation the user is a
-- member of. "Unread" = the message wasn't authored by the user AND
-- (the user has no read pointer yet OR the message sorts strictly
-- after their last_read_message_id row by (created_at, id) — the
-- tuple, not just the timestamp, so messages that share a timestamp
-- with the read pointer aren't undercounted). Soft-deleted messages
-- are excluded. Used by GET /v1/auth/me's X-Unread-Total response
-- header and by the WS heartbeat's unread_total payload.
WITH last_read AS (
    SELECT cm.conversation_id,
           cm.user_id,
           lr.created_at AS last_read_at,
           lr.id         AS last_read_id
    FROM conversation_members cm
    LEFT JOIN messages lr ON lr.id = cm.last_read_message_id
    WHERE cm.user_id = $1
)
SELECT COUNT(*)::bigint
FROM messages m
JOIN last_read r ON r.conversation_id = m.conversation_id
WHERE m.sender_id <> $1
  AND m.deleted_at IS NULL
  AND (
    r.last_read_at IS NULL
    OR (m.created_at, m.id) > (r.last_read_at, r.last_read_id)
  );

-- name: CountUnreadByConversation :many
-- Per-conversation unread count for the user, restricted to the given
-- conversation IDs. "Unread" matches CountUnreadForUser (including the
-- (created_at, id) tuple cutoff). Conversations with zero unread
-- messages are omitted from the result set. Powers the `unread_count`
-- field on each ConversationResponse (per-row badge).
WITH last_read AS (
    SELECT cm.conversation_id,
           lr.created_at AS last_read_at,
           lr.id         AS last_read_id
    FROM conversation_members cm
    LEFT JOIN messages lr ON lr.id = cm.last_read_message_id
    WHERE cm.user_id = $1
      AND cm.conversation_id = ANY($2::uuid[])
)
SELECT m.conversation_id, COUNT(*)::bigint
FROM messages m
JOIN last_read r ON r.conversation_id = m.conversation_id
WHERE m.sender_id <> $1
  AND m.deleted_at IS NULL
  AND (
    r.last_read_at IS NULL
    OR (m.created_at, m.id) > (r.last_read_at, r.last_read_id)
  )
GROUP BY m.conversation_id;
