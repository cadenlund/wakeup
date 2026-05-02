-- queries.sql for the conversations + conversation_members tables
-- (migration 0004). Constants in repo.go MUST mirror these SQL bodies
-- verbatim (§4.3).

-- name: CreateConversation :one
INSERT INTO conversations (id, type, name, avatar_url, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at;

-- name: GetConversation :one
SELECT id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at
FROM conversations
WHERE id = $1;

-- name: UpdateConversation :one
-- Patches name + avatar_url. nil-means-unchanged via COALESCE.
UPDATE conversations
SET name       = COALESCE($2, name),
    avatar_url = COALESCE($3, avatar_url)
WHERE id = $1
RETURNING id, type, name, avatar_url, created_by, created_at, updated_at, last_message_at;

-- name: TouchLastMessageAt :exec
-- Bumps last_message_at to ts when ts is greater than the current value.
-- The MessageService calls this every time a new message lands so the
-- conversation list orders by recency without firing the updated_at trigger.
UPDATE conversations
SET last_message_at = $2
WHERE id = $1 AND last_message_at < $2;

-- name: DeleteConversation :exec
DELETE FROM conversations WHERE id = $1;

-- name: ListConversationsByUser :many
-- Returns conversations the user is a member of, keyset-paginated on
-- (last_message_at DESC, id DESC) per §6.4.
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members m ON m.conversation_id = c.id
WHERE m.user_id = $1
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (c.last_message_at, c.id))
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT $4;

-- name: GetDirectByPair :one
-- Looks up the (at most one) direct conversation between two users by
-- intersecting their membership rows.
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members ma ON ma.conversation_id = c.id AND ma.user_id = $1
JOIN conversation_members mb ON mb.conversation_id = c.id AND mb.user_id = $2
WHERE c.type = 'direct';

-- name: AddMember :one
INSERT INTO conversation_members (conversation_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING conversation_id, user_id, role, joined_at, last_read_message_id;

-- name: RemoveMember :exec
DELETE FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2;

-- name: GetMember :one
SELECT conversation_id, user_id, role, joined_at, last_read_message_id
FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT conversation_id, user_id, role, joined_at, last_read_message_id
FROM conversation_members
WHERE conversation_id = $1
ORDER BY joined_at ASC, user_id ASC;

-- name: CountMembers :one
SELECT count(*) FROM conversation_members WHERE conversation_id = $1;

-- name: UpdateLastReadMessage :exec
UPDATE conversation_members
SET last_read_message_id = $3
WHERE conversation_id = $1 AND user_id = $2;
