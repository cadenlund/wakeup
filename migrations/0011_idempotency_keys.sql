-- Caches the response body + headers for write requests carrying an
-- Idempotency-Key header. See WAKEUP.md §4.8 for middleware semantics.
-- Keys are scoped per user_id (composite primary key) so two users
-- may use the same key string without collision.
--
-- response_headers is jsonb of {"Header": ["v1", "v2"]} so multi-value
-- headers (e.g. Set-Cookie) round-trip via http.Header's []string
-- semantics. Without this column the cache replay would lose
-- Content-Type and clients would parse a JSON body as text/plain.

-- +goose Up
CREATE TABLE idempotency_keys (
    key              text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 255),         -- client-supplied UUID v7 (or any unique string of 1..255 chars)
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,              -- key is scoped per user
    request_hash     bytea NOT NULL CHECK (octet_length(request_hash) = 32),            -- sha256(method + path + body); SHA-256 = 32 bytes
    response_status  int NOT NULL,
    response_headers jsonb,                                                              -- nullable: handler may not set custom headers
    response_body    bytea NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    PRIMARY KEY (user_id, key)
);
-- The composite PK above already serves user_id-prefix lookups; only need a
-- separate index for the expiry sweeper (§4.12 idempotency sweeper).
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idempotency_keys_expires_idx;
DROP TABLE IF EXISTS idempotency_keys;
