-- +goose Up
CREATE TABLE messages (
    id                  uuid PRIMARY KEY,
    conversation_id     uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id           uuid NOT NULL REFERENCES users(id),
    body                text NOT NULL,
    body_tsv            tsvector GENERATED ALWAYS AS (to_tsvector('english', body)) STORED,
    reply_to_message_id uuid REFERENCES messages(id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    edited_at           timestamptz,
    deleted_at          timestamptz
);
CREATE INDEX messages_conv_created_idx ON messages (conversation_id, created_at DESC);
CREATE INDEX messages_body_tsv_idx ON messages USING gin (body_tsv);

-- Now that `messages` exists, attach the FK that 0004_conversations.sql
-- couldn't (forward reference: conversation_members.last_read_message_id was
-- created before this table did). ON DELETE SET NULL keeps members rows alive
-- when their last-read pointer is hard-deleted (rare, but possible via admin
-- tooling).
ALTER TABLE conversation_members
    ADD CONSTRAINT conversation_members_last_read_message_fk
    FOREIGN KEY (last_read_message_id) REFERENCES messages(id) ON DELETE SET NULL;

CREATE TABLE message_attachments (
    message_id    uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    attachment_id uuid NOT NULL,
    PRIMARY KEY (message_id, attachment_id)
);

CREATE TABLE message_reads (
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id)
);
CREATE INDEX message_reads_user_idx ON message_reads (user_id);

-- +goose Down
DROP INDEX IF EXISTS message_reads_user_idx;
DROP TABLE IF EXISTS message_reads;
DROP TABLE IF EXISTS message_attachments;
-- Drop the cross-migration FK before dropping the referenced table.
ALTER TABLE conversation_members DROP CONSTRAINT IF EXISTS conversation_members_last_read_message_fk;
DROP INDEX IF EXISTS messages_body_tsv_idx;
DROP INDEX IF EXISTS messages_conv_created_idx;
DROP TABLE IF EXISTS messages;
