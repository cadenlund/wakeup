-- +goose Up
CREATE TABLE password_resets (
    token_hash bytea PRIMARY KEY,                                         -- sha256 of the token sent to email
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id);

-- +goose Down
DROP INDEX IF EXISTS password_resets_user_idx;
DROP TABLE IF EXISTS password_resets;
