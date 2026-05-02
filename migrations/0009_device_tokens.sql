-- +goose Up
CREATE TABLE device_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expo_token   text NOT NULL,
    platform     text NOT NULL CHECK (platform IN ('ios','android')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, expo_token)
);

-- +goose Down
DROP TABLE IF EXISTS device_tokens;
