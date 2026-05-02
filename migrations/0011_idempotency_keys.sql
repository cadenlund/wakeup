-- Caches the response body for write requests carrying an Idempotency-Key
-- header. See WAKEUP.md §4.8 for middleware semantics.

-- +goose Up
CREATE TABLE idempotency_keys (
    key             text PRIMARY KEY,                                                 -- client-supplied UUID v7 (or any unique string ≤ 255 chars)
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,             -- key is scoped per user
    request_hash    bytea NOT NULL,                                                   -- sha256(method + path + body)
    response_status int NOT NULL,
    response_body   bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT (now() + interval '24 hours')
);
CREATE INDEX idempotency_keys_user_idx ON idempotency_keys (user_id);
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idempotency_keys_expires_idx;
DROP INDEX IF EXISTS idempotency_keys_user_idx;
DROP TABLE IF EXISTS idempotency_keys;
