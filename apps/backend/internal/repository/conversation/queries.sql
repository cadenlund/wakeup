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
-- Returns conversations the user is a member of. Pinned-first order:
-- pinned conversations float to the top (sorted pinned_at DESC), then
-- unpinned conversations sorted by last_message_at DESC.
--
-- Pagination semantics: the cursor encodes (last_message_at, id) of the
-- last unpinned item on the previous page. The first page (cursor NULL)
-- returns ALL pinned items the user has, then the first N - pinned_count
-- unpinned items. Subsequent pages paginate ONLY the unpinned list — any
-- pinned items have already been returned.
--
-- Edge case: if a user pins more conversations than the page limit, the
-- first page returns the most recently pinned LIMIT items and subsequent
-- pages return zero rows because the cursor is NULL-on-pinned. v1
-- accepts this — typical usage is ≤5 pins. Mobile clients should expose
-- "you have 20+ pinned conversations, the rest are listed below" if/when
-- that turns into a real problem.
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members m ON m.conversation_id = c.id
WHERE m.user_id = $1
  AND ($2::timestamptz IS NULL
       OR (m.pinned_at IS NULL AND ($2::timestamptz, $3::uuid) > (c.last_message_at, c.id)))
ORDER BY (m.pinned_at IS NOT NULL) DESC,
         m.pinned_at DESC NULLS LAST,
         c.last_message_at DESC,
         c.id DESC
LIMIT $4;

-- name: GetDirectByPair :one
-- Looks up the direct conversation between two users by intersecting
-- their membership rows.
--
-- Invariant: at most one direct row per pair. The schema doesn't
-- enforce this yet (see PR #34 review) — the service layer is
-- responsible for refusing duplicate creates via the
-- LockConversationForMemberWrite pattern. To keep the repo deterministic
-- in the worst case (e.g. a backfill that produced duplicates), the
-- query is `:one` with `ORDER BY c.id ASC LIMIT 1` so two callers
-- always see the same row.
--
-- The `$1 <> $2` guard prevents a self-lookup from matching a single
-- membership row twice and pretending a 1-person conversation exists.
SELECT c.id, c.type, c.name, c.avatar_url, c.created_by,
       c.created_at, c.updated_at, c.last_message_at
FROM conversations c
JOIN conversation_members ma ON ma.conversation_id = c.id AND ma.user_id = $1
JOIN conversation_members mb ON mb.conversation_id = c.id AND mb.user_id = $2
WHERE c.type = 'direct' AND $1::uuid <> $2::uuid
ORDER BY c.id ASC
LIMIT 1;

-- name: AddMember :one
INSERT INTO conversation_members (conversation_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at;

-- name: LockConversationForMemberWrite :one
-- Step 1 of the cap-enforcing add: row-lock the conversation so
-- concurrent member writes serialize through it. Used in tandem with
-- CountMembers + AddMember inside a transaction — see the
-- AddMemberWithCap method on Queries for the full pattern.
SELECT id FROM conversations WHERE id = $1 FOR UPDATE;

-- name: RemoveMember :exec
DELETE FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2;

-- name: GetMember :one
SELECT conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at
FROM conversation_members
WHERE conversation_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at
FROM conversation_members
WHERE conversation_id = $1
ORDER BY joined_at ASC, user_id ASC;

-- name: ListMembersForConversations :many
-- Batched ListMembers across N conversations. Used by handlers
-- rendering a paginated conversation list so we make ONE
-- conversation_members query per page instead of N.
SELECT conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at
FROM conversation_members
WHERE conversation_id = ANY($1::uuid[])
ORDER BY conversation_id, joined_at ASC, user_id ASC;

-- name: CountMembers :one
SELECT count(*) FROM conversation_members WHERE conversation_id = $1;

-- name: UpdateLastReadMessage :exec
UPDATE conversation_members
SET last_read_message_id = $3
WHERE conversation_id = $1 AND user_id = $2;

-- name: SetMute :one
-- Per-member mute toggle. Pass $3 = NULL to unmute, or a future
-- timestamp to suppress pushes for this conversation until then.
-- "Forever" stores '2099-01-01' (or any far-future value) — the
-- push fanout filter compares against now() so the test is uniform.
UPDATE conversation_members
SET muted_until = $3
WHERE conversation_id = $1 AND user_id = $2
RETURNING conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at;

-- name: SetPin :one
-- Per-member pin toggle. Pass $3 = NULL to unpin, or a timestamp
-- (typically now()) to pin. Service layer enforces the "now()"
-- choice — clients send a boolean, the service converts.
UPDATE conversation_members
SET pinned_at = $3
WHERE conversation_id = $1 AND user_id = $2
RETURNING conversation_id, user_id, role, joined_at, last_read_message_id, muted_until, pinned_at;
