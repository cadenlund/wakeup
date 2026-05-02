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
-- The UNIQUE (requester_id, addressee_id) above only blocks duplicates in one
-- direction; without the next index a single logical friendship between A and B
-- could exist as both (A,B) and (B,A) with conflicting status. The pair-unique
-- index normalizes the ordering so either direction maps to the same key.
CREATE UNIQUE INDEX friendships_pair_unique_idx
    ON friendships (LEAST(requester_id, addressee_id), GREATEST(requester_id, addressee_id));

-- +goose Down
DROP INDEX IF EXISTS friendships_pair_unique_idx;
DROP INDEX IF EXISTS friendships_requester_idx;
DROP INDEX IF EXISTS friendships_addressee_idx;
DROP TABLE IF EXISTS friendships;
