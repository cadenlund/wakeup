-- +goose Up
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY,
    actor_id    uuid REFERENCES users(id),
    action      text NOT NULL,
    target_type text,
    target_id   uuid,
    metadata    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id);

-- +goose Down
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP INDEX IF EXISTS audit_log_created_idx;
DROP TABLE IF EXISTS audit_log;
