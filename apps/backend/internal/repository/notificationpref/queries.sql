-- queries.sql for the notification_preferences table (migration 0012).
-- Constants in repo.go MUST mirror these SQL bodies verbatim (§4.3).

-- name: GetOrCreate :one
-- Returns the user's row, auto-creating it with the schema defaults
-- (all booleans = true) on first call. The DO UPDATE branch is a no-op
-- write needed only because ON CONFLICT DO NOTHING with RETURNING does
-- not return the existing row — assigning user_id back to itself is
-- harmless and gives us a row to RETURN in both branches.
INSERT INTO notification_preferences (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING user_id, direct_messages, group_messages, friend_requests, calls, updated_at;

-- name: Get :one
-- Pure read used by §11 ShouldNotify so the gate doesn't force a write
-- on every notification trigger. Returns no row when the user has never
-- touched their preferences — callers map that to "default true".
SELECT user_id, direct_messages, group_messages, friend_requests, calls, updated_at
FROM notification_preferences
WHERE user_id = $1;

-- name: Patch :one
-- Patches only the fields whose pointer was non-nil in the caller (mapped
-- to NULL or value at the SQL boundary). COALESCE leaves untouched
-- columns at their current value.
UPDATE notification_preferences
SET direct_messages = COALESCE($2, direct_messages),
    group_messages  = COALESCE($3, group_messages),
    friend_requests = COALESCE($4, friend_requests),
    calls           = COALESCE($5, calls)
WHERE user_id = $1
RETURNING user_id, direct_messages, group_messages, friend_requests, calls, updated_at;
