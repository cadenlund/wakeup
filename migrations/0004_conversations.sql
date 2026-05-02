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
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user_idx ON conversation_members (user_id);

CREATE TRIGGER conversations_set_updated_at
    BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS conversations_set_updated_at ON conversations;
DROP INDEX IF EXISTS conversation_members_user_idx;
DROP TABLE IF EXISTS conversation_members;
DROP TABLE IF EXISTS conversations;
