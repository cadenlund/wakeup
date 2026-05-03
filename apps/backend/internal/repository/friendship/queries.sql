-- queries.sql for the friendships table (migration 0003).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: Create :one
-- Inserts a new pending or blocked relationship between (requester,
-- addressee). The pair-unique index in 0003 prevents both
-- (A,B) and (B,A) from coexisting — callers handle the unique-violation
-- by mapping it to apierror.Conflict.
INSERT INTO friendships (id, requester_id, addressee_id, status)
VALUES ($1, $2, $3, $4)
RETURNING id, requester_id, addressee_id, status, created_at, accepted_at;

-- name: GetByID :one
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE id = $1;

-- name: GetByPair :one
-- Finds the row between two users regardless of direction. The
-- pair-unique index guarantees at most one row exists per pair — we
-- look up by the (LEAST, GREATEST) tuple just like the index does.
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE LEAST(requester_id, addressee_id) = LEAST($1::uuid, $2::uuid)
  AND GREATEST(requester_id, addressee_id) = GREATEST($1::uuid, $2::uuid);

-- name: Accept :one
-- Transitions a pending row to accepted, stamping accepted_at. Only
-- the addressee should call this — the service layer enforces that;
-- the SQL accepts any row id.
UPDATE friendships
SET status = 'accepted',
    accepted_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING id, requester_id, addressee_id, status, created_at, accepted_at;

-- name: Delete :exec
DELETE FROM friendships WHERE id = $1;

-- name: DeleteByPair :exec
-- Used by Unfriend / Unblock to drop the row regardless of direction.
DELETE FROM friendships
WHERE LEAST(requester_id, addressee_id) = LEAST($1::uuid, $2::uuid)
  AND GREATEST(requester_id, addressee_id) = GREATEST($1::uuid, $2::uuid);

-- name: ListAcceptedByUser :many
-- Returns the user's accepted friendships, keyset-paginated on
-- (accepted_at DESC, id DESC) per §6.4. Over-fetches limit+1 so the
-- service layer can compute next_cursor + has_more.
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE status = 'accepted'
  AND (requester_id = $1 OR addressee_id = $1)
  AND ($2::timestamptz IS NULL OR ($2::timestamptz, $3::uuid) > (accepted_at, id))
ORDER BY accepted_at DESC, id DESC
LIMIT $4;

-- name: ListAllAcceptedFriendIDs :many
-- Returns the user_id of every accepted friend, unpaginated. Used by
-- the §9 presence service for fan-out: presence.update fires for
-- friends only, so we need every friend at once. The friend graph
-- is bounded by user behavior; realistic upper bound is in the
-- hundreds.
SELECT CASE WHEN requester_id = $1 THEN addressee_id ELSE requester_id END AS friend_id
FROM friendships
WHERE status = 'accepted'
  AND (requester_id = $1 OR addressee_id = $1);

-- name: ListPendingByUser :many
-- Returns pending requests where the user is on either side. Service
-- layer separates incoming vs outgoing by comparing requester_id.
SELECT id, requester_id, addressee_id, status, created_at, accepted_at
FROM friendships
WHERE status = 'pending'
  AND (requester_id = $1 OR addressee_id = $1)
ORDER BY created_at DESC, id DESC;
