-- +goose Up
CREATE TABLE conversations (
    id              uuid PRIMARY KEY,
    type            text NOT NULL CHECK (type IN ('direct','group')),
    name            text,
    avatar_url      text,
    created_by      uuid NOT NULL REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_message_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversation_members (
    conversation_id      uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id              uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role                 text NOT NULL DEFAULT 'member' CHECK (role IN ('member','admin')),
    joined_at            timestamptz NOT NULL DEFAULT now(),
    last_read_message_id uuid,
    -- Per-member mute. Future timestamp = silence pushes for this conv
    -- until then; NULL or past = audible. "Mute forever" stores a
    -- far-future date ('2099-01-01'); the push-fanout filter compares
    -- against now() so the test is uniform. Only gates the *push*; WS
    -- events still fire so the in-app unread badge etc. work normally.
    muted_until          timestamptz,
    -- Per-member pin. Non-null = pinned to the top of this user's
    -- conversation list, ordered by pinned_at DESC (most recently
    -- pinned first). Stored as a timestamp (not a bool) so the server
    -- can produce a deterministic order for multiple pins.
    pinned_at            timestamptz,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user_idx ON conversation_members (user_id);
-- Partial index for "pinned conversations of this user" lookups so the
-- list endpoint's pinned-first sort uses an index scan instead of a
-- full membership scan when most rows are unpinned.
CREATE INDEX conversation_members_user_pinned_idx
    ON conversation_members (user_id, pinned_at DESC)
    WHERE pinned_at IS NOT NULL;

CREATE TRIGGER conversations_set_updated_at
    BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS conversations_set_updated_at ON conversations;
DROP INDEX IF EXISTS conversation_members_user_pinned_idx;
DROP INDEX IF EXISTS conversation_members_user_idx;
DROP TABLE IF EXISTS conversation_members;
DROP TABLE IF EXISTS conversations;
