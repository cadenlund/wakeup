-- +goose Up
CREATE TABLE attachments (
    id           uuid PRIMARY KEY,
    uploader_id  uuid NOT NULL REFERENCES users(id),
    storage_key  text NOT NULL,
    filename     text NOT NULL,
    content_type text NOT NULL,
    size_bytes   bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS attachments;
