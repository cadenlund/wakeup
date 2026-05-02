-- +goose Up
CREATE TABLE friendships (
    id            uuid PRIMARY KEY,
    requester_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    addressee_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status        text NOT NULL CHECK (status IN ('pending','accepted','blocked')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    accepted_at   timestamptz,
    UNIQUE (requester_id, addressee_id),
    CHECK (requester_id <> addressee_id)
);
CREATE INDEX friendships_addressee_idx ON friendships (addressee_id, status);
CREATE INDEX friendships_requester_idx ON friendships (requester_id, status);

-- +goose Down
DROP INDEX IF EXISTS friendships_requester_idx;
DROP INDEX IF EXISTS friendships_addressee_idx;
DROP TABLE IF EXISTS friendships;
