-- +goose Up
CREATE TABLE attachments (
    id           uuid PRIMARY KEY,
    uploader_id  uuid NOT NULL REFERENCES users(id),
    storage_key  text NOT NULL,
    filename     text NOT NULL,
    content_type text NOT NULL,
    size_bytes   bigint NOT NULL CHECK (size_bytes >= 0),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_uploader_idx ON attachments (uploader_id);

-- 0005_messages.sql created `message_attachments` before `attachments` existed,
-- leaving `attachment_id` unconstrained. Wire the FK now (ON DELETE CASCADE so
-- removing an attachment also drops its message links).
ALTER TABLE message_attachments
    ADD CONSTRAINT message_attachments_attachment_fk
    FOREIGN KEY (attachment_id) REFERENCES attachments(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE message_attachments DROP CONSTRAINT IF EXISTS message_attachments_attachment_fk;
DROP INDEX IF EXISTS attachments_uploader_idx;
DROP TABLE IF EXISTS attachments;
